package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fp8/lite-auth-proxy/internal/auth/jwt"
)

// allowedEmail is a helper to construct a middleware that always authenticates.
func authedMiddleware(email string) func(http.Handler) http.Handler {
	v := &mockValidator{claims: jwt.Claims{"email": email}}
	return AdminAuthMiddleware(v, []string{email})
}

func buildMux(t *testing.T) *http.ServeMux {
	t.Helper()
	store := NewRuleStore()
	t.Cleanup(store.Stop)

	auth := authedMiddleware("sa@fp8devel.iam.gserviceaccount.com")
	mux := http.NewServeMux()
	mux.Handle("POST /admin/control", auth(ControlHandler(store, nil)))
	mux.Handle("GET /admin/status", auth(StatusHandler(store, nil)))
	return mux
}

func postControl(t *testing.T, mux http.Handler, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/admin/control", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer fake.token")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestControlHandler_SetRule_Returns200(t *testing.T) {
	mux := buildMux(t)
	rr := postControl(t, mux, map[string]interface{}{
		"command": "set-rule",
		"rule": map[string]interface{}{
			"ruleId":          "r1",
			"targetHost":      "api.example.com",
			"action":          "throttle",
			"maxRPM":          50,
			"durationSeconds": 600,
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp SetRuleResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RuleID != "r1" || resp.Status != "active" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestControlHandler_NoAuth_Returns401(t *testing.T) {
	store := NewRuleStore()
	t.Cleanup(store.Stop)

	auth := authedMiddleware("sa@fp8devel.iam.gserviceaccount.com")
	mux := http.NewServeMux()
	mux.Handle("POST /admin/control", auth(ControlHandler(store, nil)))

	b, _ := json.Marshal(map[string]string{"command": "remove-all"})
	req := httptest.NewRequest(http.MethodPost, "/admin/control", bytes.NewReader(b))
	// no Authorization header

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestControlHandler_InvalidJSON_Returns400(t *testing.T) {
	mux := buildMux(t)
	req := httptest.NewRequest(http.MethodPost, "/admin/control", bytes.NewReader([]byte("not-json")))
	req.Header.Set("Authorization", "Bearer fake.token")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestControlHandler_RemoveRule_ExistingRule_Returns200(t *testing.T) {
	mux := buildMux(t)

	// First create a rule
	postControl(t, mux, map[string]interface{}{
		"command": "set-rule",
		"rule": map[string]interface{}{
			"ruleId": "r-del", "targetHost": "x.example.com",
			"action": "block", "durationSeconds": 600,
		},
	})

	// Remove it
	rr := postControl(t, mux, map[string]string{
		"command": "remove-rule",
		"ruleId":  "r-del",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp RemoveResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.RulesRemoved != 1 {
		t.Fatalf("expected 1 rule removed, got %d", resp.RulesRemoved)
	}
}

func TestControlHandler_RemoveRule_NonExistent_Returns404(t *testing.T) {
	mux := buildMux(t)
	rr := postControl(t, mux, map[string]string{
		"command": "remove-rule",
		"ruleId":  "does-not-exist",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestStatusHandler_ReturnsActiveRules(t *testing.T) {
	mux := buildMux(t)

	// Create a rule
	postControl(t, mux, map[string]interface{}{
		"command": "set-rule",
		"rule": map[string]interface{}{
			"ruleId": "r-status", "targetHost": "status.example.com",
			"action": "throttle", "maxRPM": 100, "durationSeconds": 600,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/status", nil)
	req.Header.Set("Authorization", "Bearer fake.token")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp StatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Rules) != 1 {
		t.Fatalf("expected 1 rule in status, got %d", len(resp.Rules))
	}
	if resp.Rules[0].RuleID != "r-status" {
		t.Fatalf("unexpected rule ID: %s", resp.Rules[0].RuleID)
	}
}
