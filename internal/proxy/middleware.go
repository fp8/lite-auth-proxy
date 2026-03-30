package proxy

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/ratelimit"
)

type contextKey string

const authRequiredKey contextKey = "authRequired"

// RequestLogger adds structured request logs.
func RequestLogger(logger *slog.Logger, includePaths, excludePaths []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()

			next.ServeHTTP(recorder, r)

			if logger == nil {
				return
			}

			requiresAuth := ShouldAuthenticate(r.URL.Path, includePaths, excludePaths)
			result := "authorized"
			switch recorder.status {
			case http.StatusUnauthorized:
				result = "denied"
			case http.StatusTooManyRequests:
				result = "rate_limited"
			default:
				if !requiresAuth {
					result = "bypassed"
				}
			}

			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", recorder.status,
				"latency_ms", time.Since(start).Milliseconds(),
				"auth_result", result,
			)
		})
	}
}

// statusRecorder tracks response status codes.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Limiter defines the interface used by rate limit middleware.
type Limiter interface {
	Allow(ip string) (bool, int)
}

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// HeaderSanitizer strips headers with the configured auth prefix.
func HeaderSanitizer(headerPrefix string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stripHeadersWithPrefix(r.Header, headerPrefix)
			next.ServeHTTP(w, r)
		})
	}
}

// PathFilter determines whether a request path requires authentication.
func PathFilter(includePaths, excludePaths []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requiresAuth := ShouldAuthenticate(r.URL.Path, includePaths, excludePaths)
			ctx := context.WithValue(r.Context(), authRequiredKey, requiresAuth)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RateLimiter enforces per-IP rate limits for all requests regardless of auth status.
// This provides DDoS protection by capping requests per IP before any auth processing.
// For authenticated JWT paths, the handler applies additional per-identity rate limiting
// using a different key (hashed IP+sub), which does not conflict with the IP-based limit here.
func RateLimiter(limiter Limiter) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			ip := ClientIP(r)
			allowed, retryAfter := limiter.Allow(ip)
			if !allowed {
				writeRateLimitResponse(w, retryAfter)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// VertexAIRateLimit enforces the Vertex AI rate-limit bucket.
// In per-key mode the limit applies per caller identity; in global mode it applies
// to all Vertex AI traffic in aggregate.
// When bucket is nil (admin disabled) the middleware is a no-op passthrough.
func VertexAIRateLimit(bucket *ratelimit.VertexAIBucket) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bucket == nil || !ratelimit.IsVertexAIRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			identity := ratelimit.ExtractVertexAICallerIdentity(r)
			if !bucket.ShouldAllow(identity) {
				writeRateLimitResponse(w, 60)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimiter rejects requests whose body exceeds maxBytes.
// If Content-Length is present and exceeds the limit, the request is rejected immediately.
// Otherwise, the body is wrapped with http.MaxBytesReader for streaming protection.
func BodyLimiter(maxBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if maxBytes > 0 && r.Body != nil && r.Body != http.NoBody {
				if r.ContentLength > maxBytes {
					writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{
						Error:   "request_too_large",
						Message: "request body exceeds size limit",
					})
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AuthRequiredFromContext returns whether auth is required for the current request.
func AuthRequiredFromContext(ctx context.Context) bool {
	value := ctx.Value(authRequiredKey)
	requires, ok := value.(bool)
	if !ok {
		return true
	}
	return requires
}

// ShouldAuthenticate checks include/exclude path patterns.
func ShouldAuthenticate(requestPath string, includePaths, excludePaths []string) bool {
	if matchAnyPattern(requestPath, excludePaths) {
		return false
	}

	if len(includePaths) == 0 {
		return true
	}

	return matchAnyPattern(requestPath, includePaths)
}

func matchAnyPattern(requestPath string, patterns []string) bool {
	for _, pattern := range patterns {
		if pathMatches(requestPath, pattern) {
			return true
		}
	}
	return false
}

func pathMatches(requestPath, pattern string) bool {
	if pattern == "" {
		return false
	}

	if isRegexPattern(pattern) {
		re, err := regexp.Compile(pattern[1 : len(pattern)-1])
		if err != nil {
			return false
		}
		return re.MatchString(requestPath)
	}

	matched, err := path.Match(pattern, requestPath)
	if err != nil {
		return false
	}
	return matched
}

func isRegexPattern(pattern string) bool {
	return len(pattern) >= 2 && strings.HasPrefix(pattern, "/") && strings.HasSuffix(pattern, "/")
}

func stripHeadersWithPrefix(header http.Header, prefix string) {
	if prefix == "" {
		return
	}

	prefixUpper := strings.ToUpper(prefix)
	for key := range header {
		if strings.HasPrefix(strings.ToUpper(key), prefixUpper) {
			header.Del(key)
		}
	}
}

// ClientIP extracts the client IP address from the remote address.
// Note: lite-auth-proxy is designed for direct exposure without upstream proxies.
// If deployed behind a reverse proxy/load balancer, the proxy must be configured
// to set RemoteAddr appropriately (not rely on X-Forwarded-For).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}

	return r.RemoteAddr
}
