package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/admin"
	"github.com/fp8/lite-auth-proxy/internal/auth/apikey"
	"github.com/fp8/lite-auth-proxy/internal/auth/jwt"
	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/plugin"
	"github.com/fp8/lite-auth-proxy/internal/ratelimit"
	"github.com/fp8/lite-auth-proxy/internal/store"
)

type errorResponse struct {
	Error      string `json:"error"`
	Message    string `json:"message"`
	RetryAfter int    `json:"retry_after,omitempty"`
}

type handler struct {
	cfg           *config.Config
	logger        *slog.Logger
	proxy         *httputil.ReverseProxy
	healthProxy   *httputil.ReverseProxy
	jwtValidator  *jwt.Validator
	authProviders []plugin.AuthProvider
}

// ProxyDependencies exposes components created inside NewHandlerWithDeps that
// need to be accessible from main (e.g. for startup rule loading and shutdown).
type ProxyDependencies struct {
	RuleStore    store.RuleStore
	RateLimiters map[string]*ratelimit.RateLimiter
	StopFn       func()
}

// NewHandler builds the proxy handler. It is a convenience wrapper around
// NewHandlerWithDeps that discards the ProxyDependencies return value.
// All existing call sites (tests etc.) continue to work unchanged.
func NewHandler(cfg *config.Config, logger *slog.Logger) (http.Handler, error) {
	h, _, err := NewHandlerWithDeps(cfg, logger)
	return h, err
}

// NewHandlerWithDeps builds the proxy handler and returns the internal
// dependencies (rule store, rate limiters, stop function) so that callers
// (main.go) can wire the startup rule loader and trigger clean shutdown.
//
// The function is plugin-aware: if plugins are registered (via blank imports
// in the main package), it delegates middleware/route/auth setup to them.
// Otherwise it falls back to direct inline construction (backwards-compatible).
func NewHandlerWithDeps(cfg *config.Config, logger *slog.Logger) (http.Handler, *ProxyDependencies, error) {
	targetURL, err := url.Parse(cfg.Server.TargetURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid target_url: %w", err)
	}

	reverseProxy := newReverseProxy(targetURL, cfg.Server.StripPrefix, false)
	reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, proxyErr error) {
		var maxBytesErr *http.MaxBytesError
		if errors.As(proxyErr, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{
				Error:   "request_too_large",
				Message: "request body exceeds size limit",
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error:   "bad_gateway",
			Message: "upstream unreachable",
		})
		if logger != nil {
			logger.Error("upstream error", "error", proxyErr)
		}
	}

	var healthProxy *httputil.ReverseProxy
	if cfg.Server.HealthCheck.Target != "" {
		healthTarget, err := url.Parse(cfg.Server.HealthCheck.Target)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid health_check.target: %w", err)
		}
		healthProxy = newReverseProxy(healthTarget, "", true)
		healthProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, proxyErr error) {
			writeJSON(w, http.StatusBadGateway, errorResponse{
				Error:   "bad_gateway",
				Message: "upstream unreachable",
			})
			if logger != nil {
				logger.Error("health check upstream error", "error", proxyErr)
			}
		}
	}

	mux := http.NewServeMux()

	// Determine if plugins are registered.
	allPlugins := plugin.All()
	usePlugins := len(allPlugins) > 0

	var ruleStore store.RuleStore
	rateLimiters := map[string]*ratelimit.RateLimiter{}
	var authProviders []plugin.AuthProvider
	var pluginMiddlewares []Middleware

	if usePlugins {
		// --- Plugin-based pipeline assembly ---

		// Phase 1: Create default stores.
		memStore := store.NewMemoryRuleStore()
		ruleStore = memStore

		kvFactory := func(ns string) store.KeyValueStore {
			return store.NewMemoryKeyValueStore()
		}

		// Phase 2: Storage backend (optional, replaces defaults).
		storagePersist := false
		if sb := plugin.StorageBackend(); sb != nil && cfg.Storage.Enabled {
			if cv, ok := sb.(plugin.ConfigValidator); ok {
				if err := cv.ValidateConfig(cfg); err != nil {
					return nil, nil, fmt.Errorf("storage plugin %q config validation: %w", sb.Name(), err)
				}
			}
			if err := sb.Open(cfg, logger); err != nil {
				return nil, nil, fmt.Errorf("storage plugin %q: open: %w", sb.Name(), err)
			}
			persistentStore, err := sb.NewRuleStore(cfg, logger)
			if err != nil {
				return nil, nil, fmt.Errorf("storage plugin %q: new rule store: %w", sb.Name(), err)
			}
			ruleStore = persistentStore
			memStore.Stop() // no longer needed

			kvFactory = func(ns string) store.KeyValueStore {
				kv, err := sb.NewKeyValueStore(ns)
				if err != nil {
					logger.Error("storage plugin: new kv store failed", "namespace", ns, "error", err)
					return store.NewMemoryKeyValueStore() // fallback
				}
				return kv
			}
			storagePersist = true
		}

		deps := &plugin.Deps{
			Config:         cfg,
			Logger:         logger,
			RuleStore:      ruleStore,
			KeyValueStore:  kvFactory,
			StoragePersist: storagePersist,
			RateLimiters:   rateLimiters,
			AuthProviders:  nil,
		}

		// Phase 3: Config validation.
		if err := validatePluginAvailability(cfg); err != nil {
			return nil, nil, err
		}
		for _, p := range allPlugins {
			if cv, ok := p.(plugin.ConfigValidator); ok {
				if err := cv.ValidateConfig(cfg); err != nil {
					return nil, nil, fmt.Errorf("plugin %q config validation: %w", p.Name(), err)
				}
			}
		}

		// Phase 4: Route providers.
		for _, rp := range plugin.OfType[plugin.RouteProvider]() {
			if err := rp.RegisterRoutes(mux, deps); err != nil {
				return nil, nil, fmt.Errorf("plugin %q route registration: %w", rp.Name(), err)
			}
		}

		// Phase 5: Auth providers.
		authProviders = plugin.OfType[plugin.AuthProvider]()
		deps.AuthProviders = authProviders

		// Phase 6: Middleware providers.
		for _, mp := range plugin.OfType[plugin.MiddlewareProvider]() {
			mws, err := mp.BuildMiddleware(deps)
			if err != nil {
				return nil, nil, fmt.Errorf("plugin %q middleware: %w", mp.Name(), err)
			}
			for _, mw := range mws {
				pluginMiddlewares = append(pluginMiddlewares, Middleware(mw))
			}
		}

		// Phase 7: Lifecycle — start plugins.
		for _, p := range allPlugins {
			if starter, ok := p.(plugin.Starter); ok {
				if err := starter.Start(context.Background()); err != nil {
					return nil, nil, fmt.Errorf("plugin %q start: %w", p.Name(), err)
				}
			}
		}
	} else {
		// --- Legacy direct construction (no plugins registered) ---

		ipLimiter := ratelimit.NewRateLimiter(ratelimit.RateLimiterConfig{
			Name:           "ip",
			Enabled:        cfg.Security.RateLimit.Enabled,
			RequestsPerMin: cfg.Security.RateLimit.RequestsPerMin,
			BanDuration:    time.Duration(cfg.Security.RateLimit.BanForMin) * time.Minute,
			ThrottleDelay:  time.Duration(cfg.Security.RateLimit.ThrottleDelayMs) * time.Millisecond,
			MaxDelaySlots:  cfg.Security.RateLimit.MaxDelaySlots,
		})

		apikeyLimiter := ratelimit.NewRateLimiter(ratelimit.RateLimiterConfig{
			Name:           "apikey",
			Enabled:        cfg.Security.ApiKeyRateLimit.Enabled,
			RequestsPerMin: cfg.Security.ApiKeyRateLimit.RequestsPerMin,
			BanDuration:    time.Duration(cfg.Security.ApiKeyRateLimit.BanForMin) * time.Minute,
			ThrottleDelay:  time.Duration(cfg.Security.ApiKeyRateLimit.ThrottleDelayMs) * time.Millisecond,
			MaxDelaySlots:  cfg.Security.ApiKeyRateLimit.MaxDelaySlots,
		})

		jwtLimiter := ratelimit.NewRateLimiter(ratelimit.RateLimiterConfig{
			Name:           "jwt",
			Enabled:        cfg.Security.JwtRateLimit.Enabled,
			RequestsPerMin: cfg.Security.JwtRateLimit.RequestsPerMin,
			BanDuration:    time.Duration(cfg.Security.JwtRateLimit.BanForMin) * time.Minute,
			ThrottleDelay:  time.Duration(cfg.Security.JwtRateLimit.ThrottleDelayMs) * time.Millisecond,
			MaxDelaySlots:  cfg.Security.JwtRateLimit.MaxDelaySlots,
		})

		matchRules := make([]ratelimit.RequestMatchRule, len(cfg.Security.ApiKeyRateLimit.Match))
		for i, m := range cfg.Security.ApiKeyRateLimit.Match {
			matchRules[i] = ratelimit.RequestMatchRule{
				Host:   m.Host,
				Path:   m.Path,
				Header: m.Header,
			}
		}
		apiKeyMatcher, err := ratelimit.NewRequestMatcher(matchRules)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid apikey_rate_limit.match: %w", err)
		}

		rateLimiters = map[string]*ratelimit.RateLimiter{
			"ip":     ipLimiter,
			"apikey": apikeyLimiter,
			"jwt":    jwtLimiter,
		}

		var ruleChecker RuleChecker
		if cfg.Admin.Enabled {
			adminValidator := jwt.NewValidator(&cfg.Admin.JWT)
			memStore := admin.NewRuleStore()
			ruleStore = memStore
			ruleChecker = memStore

			adminAuth := admin.AdminAuthMiddleware(adminValidator, cfg.Admin.JWT.AllowedEmails, cfg.Admin.JWT.Filters)
			mux.Handle("POST /admin/control", adminAuth(admin.ControlHandler(memStore, rateLimiters)))
			mux.Handle("GET /admin/status", adminAuth(admin.StatusHandler(memStore, rateLimiters)))
		}

		skipIfJwt := cfg.Security.RateLimit.SkipIfJwtIdentified == nil || *cfg.Security.RateLimit.SkipIfJwtIdentified

		pluginMiddlewares = []Middleware{
			DynamicRuleCheck(ruleChecker),
			ApiKeyRateLimit(apikeyLimiter, apiKeyMatcher, cfg.Security.ApiKeyRateLimit.KeyHeader, cfg.Security.ApiKeyRateLimit.IncludeIP),
			JwtRateLimit(jwtLimiter, cfg.Security.JwtRateLimit.IncludeIP),
			IpRateLimit(ipLimiter, skipIfJwt),
		}
	}

	baseHandler := &handler{
		cfg:           cfg,
		logger:        logger,
		proxy:         reverseProxy,
		healthProxy:   healthProxy,
		jwtValidator:  jwt.NewValidator(&cfg.Auth.JWT),
		authProviders: authProviders,
	}

	// Assemble pipeline: core middleware + plugin middleware.
	coreMiddlewares := []Middleware{
		RequestLogger(logger, cfg.Server.IncludePaths, cfg.Server.ExcludePaths),
		BodyLimiter(cfg.Security.MaxBodyBytes),
		HeaderSanitizer(cfg.Auth.HeaderPrefix),
		PathFilter(cfg.Server.IncludePaths, cfg.Server.ExcludePaths),
	}
	allMiddleware := append(coreMiddlewares, pluginMiddlewares...)
	pipeline := applyMiddleware(baseHandler, allMiddleware...)

	healthPath := cfg.Server.HealthCheck.Path
	if healthPath == "" {
		healthPath = "/healthz"
	}

	mux.HandleFunc(healthPath, baseHandler.handleHealth)
	mux.Handle("/", pipeline)

	stopFn := func() {
		if ruleStore != nil {
			ruleStore.Stop()
		}
		if usePlugins {
			for _, p := range allPlugins {
				if stopper, ok := p.(plugin.Stopper); ok {
					if err := stopper.Stop(); err != nil {
						logger.Error("plugin stop error", "plugin", p.Name(), "error", err)
					}
				}
			}
		}
	}

	deps := &ProxyDependencies{
		RuleStore:    ruleStore,
		RateLimiters: rateLimiters,
		StopFn:       stopFn,
	}

	return mux, deps, nil
}

// validatePluginAvailability checks that config sections referencing plugins
// are backed by registered plugins. Returns a clear error if not.
func validatePluginAvailability(cfg *config.Config) error {
	checks := []struct {
		condition bool
		plugin    string
		message   string
	}{
		{
			cfg.Security.RateLimit.Enabled ||
				cfg.Security.ApiKeyRateLimit.Enabled ||
				cfg.Security.JwtRateLimit.Enabled,
			"ratelimit",
			"rate limiting is configured but the ratelimit plugin is not compiled in",
		},
		{cfg.Admin.Enabled, "admin", "admin API is configured but the admin plugin is not compiled in"},
		{cfg.Auth.APIKey.Enabled, "apikey", "API-key authentication is configured but the apikey plugin is not compiled in"},
	}

	for _, c := range checks {
		if c.condition && plugin.Get(c.plugin) == nil {
			return fmt.Errorf("%s — use the full build image or add the plugin import", c.message)
		}
	}

	if cfg.Storage.Enabled && plugin.StorageBackend() == nil {
		return fmt.Errorf("storage is enabled but no storage plugin is compiled in — use the full build image or add the plugin import")
	}

	return nil
}

func applyMiddleware(handler http.Handler, middlewares ...Middleware) http.Handler {
	wrapped := handler
	for i := len(middlewares) - 1; i >= 0; i-- {
		wrapped = middlewares[i](wrapped)
	}
	return wrapped
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requiresAuth := AuthRequiredFromContext(r.Context())
	if !requiresAuth {
		h.forward(w, r)
		return
	}

	// Rate-limit-only mode: both auth methods disabled → pass through without credential check
	if !h.cfg.Auth.JWT.Enabled && !h.cfg.Auth.APIKey.Enabled {
		h.forward(w, r)
		return
	}

	ip := ClientIP(r)
	bearerToken, hasBearer := extractBearerToken(r.Header.Get("Authorization"))
	if h.cfg.Auth.JWT.Enabled && hasBearer {
		claims, err := h.jwtValidator.ValidateToken(bearerToken)
		if err != nil {
			h.respondJWTError(w, err)
			return
		}

		if err := jwt.EvaluateFilters(claims, h.cfg.Auth.JWT.Filters); err != nil {
			writeJSON(w, http.StatusUnauthorized, errorResponse{
				Error:   "unauthorized",
				Message: "access denied",
			})
			return
		}

		// Check explicit email allowlist when configured.
		// An empty AllowedEmails means no restriction — skip the check.
		if len(h.cfg.Auth.JWT.AllowedEmails) > 0 {
			if !isEmailAllowed(claims, h.cfg.Auth.JWT.AllowedEmails) {
				writeJSON(w, http.StatusUnauthorized, errorResponse{
					Error:   "unauthorized",
					Message: "access denied",
				})
				return
			}
		}

		_ = ip // IP used by middleware-level rate limiting; no handler-level rate limiting needed.

		mappedHeaders := jwt.MapClaims(claims, h.cfg.Auth.JWT.Mappings, h.cfg.Auth.HeaderPrefix)
		applyHeaders(r.Header, mappedHeaders)
		h.forward(w, r)
		return
	}

	// Try plugin-based auth providers (e.g. API-key plugin).
	if len(h.authProviders) > 0 {
		for _, ap := range h.authProviders {
			headers, err := ap.Authenticate(r, &h.cfg.Auth)
			if err != nil {
				if errors.Is(err, apikey.ErrMissingAPIKey) {
					continue // this provider doesn't apply; try next
				}
				writeJSON(w, http.StatusUnauthorized, errorResponse{
					Error:   "unauthorized",
					Message: "invalid api key",
				})
				return
			}
			if headers != nil {
				applyHeaders(r.Header, headers)
				h.forward(w, r)
				return
			}
			// headers == nil && err == nil means provider is disabled; try next.
		}
	}

	// Legacy inline API-key auth (when no apikey plugin registered).
	if len(h.authProviders) == 0 && h.cfg.Auth.APIKey.Enabled {
		headers, err := apikey.ValidateAPIKey(r, &h.cfg.Auth)
		if err != nil {
			if errors.Is(err, apikey.ErrMissingAPIKey) {
				writeJSON(w, http.StatusUnauthorized, errorResponse{
					Error:   "unauthorized",
					Message: "missing credentials",
				})
				return
			}
			writeJSON(w, http.StatusUnauthorized, errorResponse{
				Error:   "unauthorized",
				Message: "invalid api key",
			})
			return
		}

		applyHeaders(r.Header, headers)
		h.forward(w, r)
		return
	}

	writeJSON(w, http.StatusUnauthorized, errorResponse{
		Error:   "unauthorized",
		Message: "missing credentials",
	})
}

func (h *handler) forward(w http.ResponseWriter, r *http.Request) {
	h.proxy.ServeHTTP(w, r)
}

func (h *handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Server.HealthCheck.Target == "" || h.healthProxy == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	h.healthProxy.ServeHTTP(w, r)
}

func (h *handler) respondJWTError(w http.ResponseWriter, err error) {
	status, message := mapJWTError(err)
	responseError := "unauthorized"
	if status == http.StatusBadGateway {
		responseError = "bad_gateway"
	}
	writeJSON(w, status, errorResponse{
		Error:   responseError,
		Message: message,
	})
}

func mapJWTError(err error) (int, string) {
	msg := err.Error()

	switch {
	case strings.Contains(msg, "token expired"):
		return http.StatusUnauthorized, "token expired"
	case strings.Contains(msg, "not yet valid"):
		return http.StatusUnauthorized, "token not yet valid"
	case strings.Contains(msg, "invalid token signature"):
		return http.StatusUnauthorized, "invalid token signature"
	case strings.Contains(msg, "invalid issuer") || strings.Contains(msg, "invalid audience") || strings.Contains(msg, "iss claim") || strings.Contains(msg, "aud claim"):
		return http.StatusUnauthorized, "invalid token claims"
	case strings.Contains(msg, "kid not found") || strings.Contains(msg, "invalid token format") || strings.Contains(msg, "failed to decode") || strings.Contains(msg, "failed to parse"):
		return http.StatusUnauthorized, "invalid token format"
	case strings.Contains(msg, "failed to get public key") || strings.Contains(msg, "jwks") || strings.Contains(msg, "oidc"):
		return http.StatusBadGateway, "unable to validate token"
	default:
		return http.StatusUnauthorized, "invalid token"
	}
}

func extractBearerToken(authHeader string) (string, bool) {
	if authHeader == "" {
		return "", false
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", true
	}

	return token, true
}

func applyHeaders(header http.Header, values map[string]string) {
	for key, value := range values {
		header.Set(key, value)
	}
}

func writeRateLimitResponse(w http.ResponseWriter, retryAfter int) {
	if retryAfter > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	}
	writeJSON(w, http.StatusTooManyRequests, errorResponse{
		Error:      "rate_limited",
		Message:    "too many requests",
		RetryAfter: retryAfter,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}

	encoder := json.NewEncoder(w)
	if err := encoder.Encode(payload); err != nil {
		_, _ = io.WriteString(w, "{}")
	}
}

func newReverseProxy(target *url.URL, stripPrefix string, useExactPath bool) *httputil.ReverseProxy {
	director := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host

		if useExactPath {
			// For health checks: use exact target path and query
			req.URL.Path = target.Path
			req.URL.RawQuery = target.RawQuery
		} else {
			// For regular proxying: optionally strip prefix from incoming request path
			if stripPrefix != "" && strings.HasPrefix(req.URL.Path, stripPrefix) {
				req.URL.Path = strings.TrimPrefix(req.URL.Path, stripPrefix)
				if req.URL.Path == "" {
					req.URL.Path = "/"
				}
			}
		}
	}

	return &httputil.ReverseProxy{Director: director}
}

// isEmailAllowed returns true if the "email" claim in claims matches one of the
// allowedEmails entries (case-insensitive). Returns false when email is absent.
func isEmailAllowed(claims jwt.Claims, allowedEmails []string) bool {
	email, _ := claims["email"].(string)
	if email == "" {
		return false
	}
	emailLower := strings.ToLower(email)
	for _, allowed := range allowedEmails {
		if strings.ToLower(allowed) == emailLower {
			return true
		}
	}
	return false
}
