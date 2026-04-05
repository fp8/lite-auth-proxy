package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

const (
	authRequiredKey    contextKey = "authRequired"
	jwtIdentifiedKey   contextKey = "jwtIdentified"
)

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

// IpRateLimit enforces per-IP rate limits for all requests regardless of auth status.
// This provides DDoS protection by capping requests per IP before any auth processing.
// When skipIfJwtIdentified is true, requests that carry a JWT sub claim (identified by
// JwtRateLimit upstream) bypass the IP check — they are already governed by the JWT limiter.
func IpRateLimit(limiter *ratelimit.RateLimiter, skipIfJwtIdentified bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			if skipIfJwtIdentified {
				if identified, _ := r.Context().Value(jwtIdentifiedKey).(bool); identified {
					next.ServeHTTP(w, r)
					return
				}
			}

			ip := ClientIP(r)
			allowed, retryAfter := limiter.Allow(ip)
			if !allowed {
				handleRateLimited(w, retryAfter, limiter)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ApiKeyRateLimit enforces per-API-key rate limits for requests matching the configured rules.
// When no rules match, the middleware is a passthrough.
// keyHeader is the primary header to extract the API key from (e.g. "x-goog-api-key").
func ApiKeyRateLimit(limiter *ratelimit.RateLimiter, matcher *ratelimit.RequestMatcher, keyHeader string, includeIP bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil || matcher == nil || !matcher.Matches(r) {
				next.ServeHTTP(w, r)
				return
			}

			key := extractApiKey(r, keyHeader)
			if key == "" {
				key = ClientIP(r) // fallback to IP if no API key found
			} else {
				key = "k:" + hashIdentity(key)
			}

			if includeIP {
				key = ClientIP(r) + ":" + key
			}

			allowed, retryAfter := limiter.Allow(key)
			if !allowed {
				handleRateLimited(w, retryAfter, limiter)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// JwtRateLimit enforces per-JWT-identity rate limits using the Bearer token's sub claim.
// Uses a non-validating JWT parse (rate limiting runs before expensive JWT validation).
// When no Bearer token or sub claim is present, the middleware is a passthrough.
func JwtRateLimit(limiter *ratelimit.RateLimiter, includeIP bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			sub := extractBearerSub(r.Header.Get("Authorization"))
			if sub == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Mark JWT identity in context so IpRateLimit can skip when configured.
			ctx := context.WithValue(r.Context(), jwtIdentifiedKey, true)
			r = r.WithContext(ctx)

			key := "s:" + sub
			if includeIP {
				key = ClientIP(r) + ":" + key
			}

			allowed, retryAfter := limiter.Allow(key)
			if !allowed {
				handleRateLimited(w, retryAfter, limiter)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// handleRateLimited applies the optional throttle delay before writing a 429 response.
func handleRateLimited(w http.ResponseWriter, retryAfter int, limiter *ratelimit.RateLimiter) {
	if acquired, sem := limiter.TryAcquireDelaySlot(); acquired {
		defer limiter.ReleaseDelaySlot(sem)
		time.Sleep(limiter.ThrottleDelay())
	}
	writeRateLimitResponse(w, retryAfter)
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

// extractApiKey extracts an API key from the request, checking the given header
// name first, then the "key" query parameter.
func extractApiKey(r *http.Request, keyHeader string) string {
	if keyHeader != "" {
		if key := r.Header.Get(keyHeader); key != "" {
			return key
		}
	}
	return r.URL.Query().Get("key")
}

// hashIdentity returns the first 16 base64url chars of the SHA-256 of raw.
func hashIdentity(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(h[:12]) // 96 bits → 16 chars
}

// extractBearerSub does a non-validating parse of a Bearer JWT to read the sub claim.
// Returns "" if the header is absent, not a JWT, or has no sub claim.
func extractBearerSub(authHeader string) string {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	sub, _ := claims["sub"].(string)
	return sub
}
