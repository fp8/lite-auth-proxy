package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/ratelimit"
)

// VertexAIBucket is the interface for controlling and querying the Vertex AI rate-limit bucket.
// Implemented by *ratelimit.VertexAIBucket.
type VertexAIBucket interface {
	SetMaxRPM(maxRPM int, perKey bool)
	Disable()
	GetStatus() *ratelimit.VertexAIStatus
}

// ControlHandler handles POST /admin/control requests.
func ControlHandler(store *RuleStore, vertexBucket VertexAIBucket) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ControlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAdminJSON(w, http.StatusBadRequest, map[string]string{
				"error":   "bad_request",
				"message": "invalid JSON",
			})
			return
		}

		switch req.Command {
		case "set-rule":
			handleSetRule(w, store, vertexBucket, req.Rule)
		case "remove-rule":
			handleRemoveRule(w, store, vertexBucket, req.RuleID)
		case "remove-all":
			handleRemoveAll(w, store, vertexBucket)
		default:
			writeAdminJSON(w, http.StatusBadRequest, map[string]string{
				"error":   "bad_request",
				"message": fmt.Sprintf("unknown command: %q", req.Command),
			})
		}
	})
}

func handleSetRule(w http.ResponseWriter, store *RuleStore, vertexBucket VertexAIBucket, rule *Rule) {
	if rule == nil {
		writeAdminJSON(w, http.StatusBadRequest, map[string]string{
			"error": "bad_request", "message": "rule is required for set-rule",
		})
		return
	}
	if err := validateRule(rule); err != nil {
		writeAdminJSON(w, http.StatusBadRequest, map[string]string{
			"error": "bad_request", "message": err.Error(),
		})
		return
	}
	if err := store.SetRule(rule); err != nil {
		writeAdminJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "internal_error", "message": err.Error(),
		})
		return
	}

	// Wire Vertex AI bucket if this rule targets a Vertex AI path.
	if vertexBucket != nil && rule.Action == "throttle" && isVertexAIPath(rule.PathPattern) {
		vertexBucket.SetMaxRPM(rule.MaxRPM, rule.RateByKey)
	}

	writeAdminJSON(w, http.StatusOK, SetRuleResponse{
		RuleID:    rule.RuleID,
		Status:    "active",
		ExpiresAt: rule.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func handleRemoveRule(w http.ResponseWriter, store *RuleStore, vertexBucket VertexAIBucket, ruleID string) {
	if ruleID == "" {
		writeAdminJSON(w, http.StatusBadRequest, map[string]string{
			"error": "bad_request", "message": "ruleId is required for remove-rule",
		})
		return
	}
	found, err := store.RemoveRule(ruleID)
	if err != nil {
		writeAdminJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "internal_error", "message": err.Error(),
		})
		return
	}
	if !found {
		writeAdminJSON(w, http.StatusNotFound, map[string]string{
			"error": "not_found", "message": "rule not found",
		})
		return
	}
	if vertexBucket != nil {
		vertexBucket.Disable()
	}
	writeAdminJSON(w, http.StatusOK, RemoveResponse{
		Status:       "ok",
		RulesRemoved: 1,
	})
}

func handleRemoveAll(w http.ResponseWriter, store *RuleStore, vertexBucket VertexAIBucket) {
	count := store.RemoveAll()
	if vertexBucket != nil {
		vertexBucket.Disable()
	}
	writeAdminJSON(w, http.StatusOK, RemoveResponse{
		Status:       "ok",
		RulesRemoved: count,
	})
}

// StatusHandler handles GET /admin/status requests.
func StatusHandler(store *RuleStore, vertexBucket VertexAIBucket) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := StatusResponse{
			Rules:    store.GetStatus(),
			VertexAI: nil,
		}
		if vertexBucket != nil {
			resp.VertexAI = vertexBucket.GetStatus()
		}
		writeAdminJSON(w, http.StatusOK, resp)
	})
}

func validateRule(rule *Rule) error {
	if rule.RuleID == "" {
		return fmt.Errorf("ruleId is required")
	}
	if rule.TargetHost == "" {
		return fmt.Errorf("targetHost is required")
	}
	switch rule.Action {
	case "throttle", "block", "allow":
	default:
		return fmt.Errorf("action must be one of: throttle, block, allow")
	}
	if rule.Action == "throttle" && rule.MaxRPM <= 0 {
		return fmt.Errorf("maxRPM must be > 0 when action is throttle")
	}
	if rule.DurationSeconds <= 0 {
		return fmt.Errorf("durationSeconds must be > 0")
	}
	return nil
}

// isVertexAIPath returns true if the path pattern targets Vertex AI endpoints.
func isVertexAIPath(pattern *string) bool {
	if pattern == nil {
		return false
	}
	return strings.Contains(*pattern, "/v1/projects/") ||
		strings.Contains(*pattern, "aiplatform.googleapis.com")
}

func writeAdminJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}
