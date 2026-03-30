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
// configured issuer/audience, and verifies the email claim is in allowedEmails.
func AdminAuthMiddleware(validator TokenValidator, allowedEmails []string) func(http.Handler) http.Handler {
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

			email, _ := claims["email"].(string)
			if !isEmailAllowed(email, allowedEmails) {
				writeAdminJSON(w, http.StatusUnauthorized, map[string]string{
					"error":   "unauthorized",
					"message": "Invalid or missing identity token",
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isEmailAllowed(email string, allowedEmails []string) bool {
	if len(allowedEmails) == 0 || email == "" {
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
