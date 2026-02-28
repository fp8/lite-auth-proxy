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

// NewHandler builds the proxy handler with middleware and health checks.
func NewHandler(cfg *config.Config, logger *slog.Logger) (http.Handler, error) {
	targetURL, err := url.Parse(cfg.Server.TargetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid target_url: %w", err)
	}

	reverseProxy := newReverseProxy(targetURL, cfg.Server.StripPrefix)
	reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, proxyErr error) {
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
			return nil, fmt.Errorf("invalid health_check.target: %w", err)
		}
		healthProxy = newHealthProxy(healthTarget)
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

	pipeline := applyMiddleware(baseHandler,
		RequestLogger(logger, cfg.Server.IncludePaths, cfg.Server.ExcludePaths),
		HeaderSanitizer(cfg.Auth.HeaderPrefix),
		PathFilter(cfg.Server.IncludePaths, cfg.Server.ExcludePaths),
		RateLimiter(limiter),
	)

	healthPath := cfg.Server.HealthCheck.Path
	if healthPath == "" {
		healthPath = "/healthz"
	}

	mux := http.NewServeMux()
	mux.HandleFunc(healthPath, baseHandler.handleHealth)
	mux.Handle("/", pipeline)

	return mux, nil
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

		// Apply rate limiting based on IP for API key auth
		if h.limiter != nil {
			allowed, retryAfter := h.limiter.Allow(ip)
			if !allowed {
				writeRateLimitResponse(w, retryAfter)
				return
			}
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

func newReverseProxy(target *url.URL, stripPrefix string) *httputil.ReverseProxy {
	director := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host

		if stripPrefix != "" && strings.HasPrefix(req.URL.Path, stripPrefix) {
			req.URL.Path = strings.TrimPrefix(req.URL.Path, stripPrefix)
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
		}
	}

	return &httputil.ReverseProxy{Director: director}
}

func newHealthProxy(target *url.URL) *httputil.ReverseProxy {
	director := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		req.URL.Path = target.Path
		req.URL.RawQuery = target.RawQuery
	}

	return &httputil.ReverseProxy{Director: director}
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
