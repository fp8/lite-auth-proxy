package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fp8/lite-auth-proxy/internal/auth/jwt"
)

// mockValidator implements TokenValidator for testing.
type mockValidator struct {
	claims jwt.Claims
	err    error
}

func (m *mockValidator) ValidateToken(_ string) (jwt.Claims, error) {
	return m.claims, m.err
}

func okHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestAdminAuthMiddleware_ValidToken_AllowedEmail(t *testing.T) {
	v := &mockValidator{claims: jwt.Claims{"email": "sa@fp8devel.iam.gserviceaccount.com"}}
	mw := AdminAuthMiddleware(v, []string{"sa@fp8devel.iam.gserviceaccount.com"})

	req := httptest.NewRequest(http.MethodGet, "/admin/status", nil)
	req.Header.Set("Authorization", "Bearer fake.jwt.token")

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAdminAuthMiddleware_MissingAuthHeader(t *testing.T) {
	v := &mockValidator{claims: jwt.Claims{"email": "sa@fp8devel.iam.gserviceaccount.com"}}
	mw := AdminAuthMiddleware(v, []string{"sa@fp8devel.iam.gserviceaccount.com"})

	req := httptest.NewRequest(http.MethodGet, "/admin/status", nil)
	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAdminAuthMiddleware_ValidToken_EmailNotAllowed(t *testing.T) {
	v := &mockValidator{claims: jwt.Claims{"email": "other@example.com"}}
	mw := AdminAuthMiddleware(v, []string{"sa@fp8devel.iam.gserviceaccount.com"})

	req := httptest.NewRequest(http.MethodGet, "/admin/status", nil)
	req.Header.Set("Authorization", "Bearer fake.jwt.token")

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAdminAuthMiddleware_InvalidToken(t *testing.T) {
	v := &mockValidator{err: fmt.Errorf("token expired")}
	mw := AdminAuthMiddleware(v, []string{"sa@fp8devel.iam.gserviceaccount.com"})

	req := httptest.NewRequest(http.MethodGet, "/admin/status", nil)
	req.Header.Set("Authorization", "Bearer bad.token")

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}
