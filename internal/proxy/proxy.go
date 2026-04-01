package proxy

import (
	"crypto/sha256"
	"encoding/base64"
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
	"github.com/fp8/lite-auth-proxy/internal/ratelimit"
)

type errorResponse struct {
	Error      string `json:"error"`
	Message    string `json:"message"`
	RetryAfter int    `json:"retry_after,omitempty"`
}

type handler struct {
	cfg          *config.Config
	logger       *slog.Logger
	proxy        *httputil.ReverseProxy
	healthProxy  *httputil.ReverseProxy
	jwtValidator *jwt.Validator
	limiter      *ratelimit.Limiter
}

// ProxyDependencies exposes components created inside NewHandlerWithDeps that
// need to be accessible from main (e.g. for startup rule loading and shutdown).
type ProxyDependencies struct {
	RuleStore    *admin.RuleStore
	VertexBucket *ratelimit.VertexAIBucket
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
// dependencies (rule store, Vertex AI bucket, stop function) so that callers
// (main.go) can wire the startup rule loader and trigger clean shutdown.
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

	limiter := ratelimit.NewLimiter(
		cfg.Security.RateLimit.Enabled,
		cfg.Security.RateLimit.RequestsPerMin,
		time.Duration(cfg.Security.RateLimit.BanForMin)*time.Minute,
	)

	baseHandler := &handler{
		cfg:          cfg,
		logger:       logger,
		proxy:        reverseProxy,
		healthProxy:  healthProxy,
		jwtValidator: jwt.NewValidator(&cfg.Auth.JWT),
		limiter:      limiter,
	}

	// Admin control-plane (Steps 01, 03, 04).
	var ruleChecker RuleChecker              // nil when admin disabled
	var ruleStore *admin.RuleStore           // nil when admin disabled
	var vertexBucket *ratelimit.VertexAIBucket // nil when admin disabled

	mux := http.NewServeMux()

	if cfg.Admin.Enabled {
		adminValidator := jwt.NewValidator(&cfg.Admin.JWT)
		ruleStore = admin.NewRuleStore()
		ruleChecker = ruleStore
		vertexBucket = ratelimit.NewVertexAIBucket()

		adminAuth := admin.AdminAuthMiddleware(adminValidator, cfg.Admin.JWT.AllowedEmails, cfg.Admin.JWT.Filters)
		mux.Handle("POST /admin/control", adminAuth(admin.ControlHandler(ruleStore, vertexBucket)))
		mux.Handle("GET /admin/status", adminAuth(admin.StatusHandler(ruleStore, vertexBucket)))
	}

	pipeline := applyMiddleware(baseHandler,
		RequestLogger(logger, cfg.Server.IncludePaths, cfg.Server.ExcludePaths),
		BodyLimiter(cfg.Security.MaxBodyBytes),
		HeaderSanitizer(cfg.Auth.HeaderPrefix),
		PathFilter(cfg.Server.IncludePaths, cfg.Server.ExcludePaths),
		DynamicRuleCheck(ruleChecker),    // Step 02 — no-op when ruleChecker is nil
		VertexAIRateLimit(vertexBucket), // Step 03 — no-op when vertexBucket is nil
		RateLimiter(limiter),
	)

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
	}

	deps := &ProxyDependencies{
		RuleStore:    ruleStore,
		VertexBucket: vertexBucket,
		StopFn:       stopFn,
	}

	return mux, deps, nil
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

		// Apply rate limiting based on JWT sub claim
		sub, _ := claims["sub"].(string)
		rateLimitKey := hashKey(ip, sub)
		if h.limiter != nil {
			allowed, retryAfter := h.limiter.Allow(rateLimitKey)
			if !allowed {
				writeRateLimitResponse(w, retryAfter)
				return
			}
		}

		mappedHeaders := jwt.MapClaims(claims, h.cfg.Auth.JWT.Mappings, h.cfg.Auth.HeaderPrefix)
		applyHeaders(r.Header, mappedHeaders)
		h.forward(w, r)
		return
	}

	if h.cfg.Auth.APIKey.Enabled {
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

		// IP-based rate limiting is already enforced by the RateLimiter middleware.
		// No additional handler-level rate limiting needed for API key auth.

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

// hashKey hashes an IP-sub pair for memory-efficient rate limit tracking.
// Uses SHA256 to create a fixed-size identifier from potentially long sub claim values.
// Returns base64url encoding (43 chars) instead of hex (64 chars) for better memory efficiency.
func hashKey(ip, sub string) string {
	if sub == "" {
		return ip
	}

	h := sha256.Sum256([]byte(ip + ":" + sub))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
