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

// makeReq builds a GET /admin/status request with an optional Bearer token.
func makeReq(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/admin/status", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// --- AllowedEmails tests ---

func TestAdminAuth_AllowedEmails_Match(t *testing.T) {
	v := &mockValidator{claims: jwt.Claims{"email": "sa@fp8devel.iam.gserviceaccount.com"}}
	mw := AdminAuthMiddleware(v, []string{"sa@fp8devel.iam.gserviceaccount.com"}, nil)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("fake.jwt.token"))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAdminAuth_AllowedEmails_NoMatch(t *testing.T) {
	v := &mockValidator{claims: jwt.Claims{"email": "other@example.com"}}
	mw := AdminAuthMiddleware(v, []string{"sa@fp8devel.iam.gserviceaccount.com"}, nil)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("fake.jwt.token"))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAdminAuth_AllowedEmails_Empty_NoRestriction(t *testing.T) {
	// Empty AllowedEmails should NOT restrict access; any authenticated token passes.
	v := &mockValidator{claims: jwt.Claims{"email": "anyone@example.com"}}
	mw := AdminAuthMiddleware(v, []string{} /* no list */, nil)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("fake.jwt.token"))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 when AllowedEmails is empty, got %d", rr.Code)
	}
}

func TestAdminAuth_AllowedEmails_Nil_NoRestriction(t *testing.T) {
	// Nil AllowedEmails also means no restriction.
	v := &mockValidator{claims: jwt.Claims{"email": "anyone@example.com"}}
	mw := AdminAuthMiddleware(v, nil, nil)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("fake.jwt.token"))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 when AllowedEmails is nil, got %d", rr.Code)
	}
}

// --- Filters tests ---

func TestAdminAuth_FiltersOnly_Match(t *testing.T) {
	// No AllowedEmails; access controlled exclusively via filters (e.g. hd domain).
	v := &mockValidator{claims: jwt.Claims{"email": "user@company.com", "hd": "company.com"}}
	filters := map[string]string{"hd": "company.com"}
	mw := AdminAuthMiddleware(v, nil, filters)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("fake.jwt.token"))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with matching filter, got %d", rr.Code)
	}
}

func TestAdminAuth_FiltersOnly_NoMatch(t *testing.T) {
	v := &mockValidator{claims: jwt.Claims{"email": "outsider@other.com", "hd": "other.com"}}
	filters := map[string]string{"hd": "company.com"}
	mw := AdminAuthMiddleware(v, nil, filters)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("fake.jwt.token"))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when filter does not match, got %d", rr.Code)
	}
}

func TestAdminAuth_FiltersOnly_RegexEmailDomain(t *testing.T) {
	// Regex filter on email claim — useful for domain-level allow.
	v := &mockValidator{claims: jwt.Claims{"email": "alice@company.com"}}
	filters := map[string]string{"email": `/.*@company\.com$/`}
	mw := AdminAuthMiddleware(v, nil, filters)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("fake.jwt.token"))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with matching regex email filter, got %d", rr.Code)
	}
}

func TestAdminAuth_FiltersOnly_RegexEmailDomain_NoMatch(t *testing.T) {
	v := &mockValidator{claims: jwt.Claims{"email": "alice@other.com"}}
	filters := map[string]string{"email": `/.*@company\.com$/`}
	mw := AdminAuthMiddleware(v, nil, filters)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("fake.jwt.token"))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when email does not match regex filter, got %d", rr.Code)
	}
}

// --- Combined AllowedEmails + Filters tests ---

func TestAdminAuth_CombinedFiltersAndAllowedEmails_BothPass(t *testing.T) {
	// Filters evaluated first, then AllowedEmails. Both must pass.
	v := &mockValidator{claims: jwt.Claims{"email": "sa@company.com", "hd": "company.com"}}
	filters := map[string]string{"hd": "company.com"}
	mw := AdminAuthMiddleware(v, []string{"sa@company.com"}, filters)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("fake.jwt.token"))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 when both filter and email match, got %d", rr.Code)
	}
}

func TestAdminAuth_CombinedFiltersAndAllowedEmails_FilterFails(t *testing.T) {
	v := &mockValidator{claims: jwt.Claims{"email": "sa@company.com", "hd": "other.com"}}
	filters := map[string]string{"hd": "company.com"}
	mw := AdminAuthMiddleware(v, []string{"sa@company.com"}, filters)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("fake.jwt.token"))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when filter fails even if email is allowed, got %d", rr.Code)
	}
}

func TestAdminAuth_CombinedFiltersAndAllowedEmails_EmailFails(t *testing.T) {
	v := &mockValidator{claims: jwt.Claims{"email": "stranger@company.com", "hd": "company.com"}}
	filters := map[string]string{"hd": "company.com"}
	mw := AdminAuthMiddleware(v, []string{"sa@company.com"}, filters)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("fake.jwt.token"))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when filter passes but email is not in list, got %d", rr.Code)
	}
}

// --- Token / header error tests ---

func TestAdminAuth_MissingAuthHeader(t *testing.T) {
	v := &mockValidator{claims: jwt.Claims{"email": "sa@fp8devel.iam.gserviceaccount.com"}}
	mw := AdminAuthMiddleware(v, []string{"sa@fp8devel.iam.gserviceaccount.com"}, nil)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("" /* no token */))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAdminAuth_InvalidToken(t *testing.T) {
	v := &mockValidator{err: fmt.Errorf("token expired")}
	mw := AdminAuthMiddleware(v, []string{"sa@fp8devel.iam.gserviceaccount.com"}, nil)

	rr := httptest.NewRecorder()
	mw(okHandler(t)).ServeHTTP(rr, makeReq("bad.token"))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}
