package admin

import (
	"net/http"
	"strings"

	"github.com/fp8/lite-auth-proxy/internal/auth/jwt"
)

// TokenValidator validates a JWT token and returns its claims.
// Satisfied by *jwt.Validator.
type TokenValidator interface {
	ValidateToken(token string) (jwt.Claims, error)
}

// AdminAuthMiddleware returns middleware that validates GCP service account identity tokens.
// It checks the Authorization: Bearer <token> header, validates the token against the
// configured issuer/audience, then applies two optional access controls in order:
//
//  1. filters — claim-based rules (exact match or regex), evaluated via jwt.EvaluateFilters.
//     An empty map means no filter restriction.
//  2. allowedEmails — explicit email whitelist.
//     An empty slice means no email restriction.
//
// At least one of filters or allowedEmails must be non-empty when admin is enabled
// (enforced at config validation time, not here).
func AdminAuthMiddleware(validator TokenValidator, allowedEmails []string, filters map[string]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				writeAdminJSON(w, http.StatusUnauthorized, map[string]string{
					"error":   "unauthorized",
					"message": "Invalid or missing identity token",
				})
				return
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token == "" {
				writeAdminJSON(w, http.StatusUnauthorized, map[string]string{
					"error":   "unauthorized",
					"message": "Invalid or missing identity token",
				})
				return
			}

			claims, err := validator.ValidateToken(token)
			if err != nil {
				writeAdminJSON(w, http.StatusUnauthorized, map[string]string{
					"error":   "unauthorized",
					"message": "Invalid or missing identity token",
				})
				return
			}

			// Evaluate claim filters (e.g. hd domain, email regex).
			// Skipped when filters map is empty.
			if len(filters) > 0 {
				if err := jwt.EvaluateFilters(claims, filters); err != nil {
					writeAdminJSON(w, http.StatusUnauthorized, map[string]string{
						"error":   "unauthorized",
						"message": "Invalid or missing identity token",
					})
					return
				}
			}

			// Evaluate explicit email allowlist.
			// Skipped when allowedEmails is empty (no restriction).
			if len(allowedEmails) > 0 {
				email, _ := claims["email"].(string)
				if !isEmailAllowed(email, allowedEmails) {
					writeAdminJSON(w, http.StatusUnauthorized, map[string]string{
						"error":   "unauthorized",
						"message": "Invalid or missing identity token",
					})
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isEmailAllowed reports whether email is in the allowedEmails list (case-insensitive).
// Returns false only when a non-empty list is provided and email is not in it.
func isEmailAllowed(email string, allowedEmails []string) bool {
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
