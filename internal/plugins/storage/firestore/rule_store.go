package firestore

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/fp8/lite-auth-proxy/internal/store"
)

type ruleDoc struct {
	RuleID          string    `firestore:"ruleId"`
	TargetHost      string    `firestore:"targetHost"`
	Action          string    `firestore:"action"`
	MaxRPM          int       `firestore:"maxRPM,omitempty"`
	PathPattern     *string   `firestore:"pathPattern,omitempty"`
	RateByKey       bool      `firestore:"rateByKey,omitempty"`
	Limiter         string    `firestore:"limiter,omitempty"`
	ThrottleDelayMs int       `firestore:"throttleDelayMs,omitempty"`
	MaxDelaySlots   int       `firestore:"maxDelaySlots,omitempty"`
	DurationSeconds int       `firestore:"durationSeconds"`
	ExpiresAt       time.Time `firestore:"expiresAt"`
	CreatedAt       time.Time `firestore:"createdAt"`
	UpdatedAt       time.Time `firestore:"updatedAt"`
}

// FirestoreRuleStore implements store.RuleStore backed by Firestore.
// It maintains an internal in-memory cache for the hot path (ShouldAllow)
// and uses a Firestore snapshot listener for cross-instance synchronization.
type FirestoreRuleStore struct {
	client     *firestore.Client
	collection string
	logger     *slog.Logger

	mu      sync.RWMutex
	rules   map[string]*store.Rule
	stopCh  chan struct{}
	stopped bool
	cancel  context.CancelFunc
}

// NewFirestoreRuleStore creates a rule store backed by Firestore.
// It loads all existing non-expired rules on startup via a snapshot listener
// and keeps the cache synchronized in real-time across instances.
func NewFirestoreRuleStore(client *firestore.Client, prefix string, logger *slog.Logger) (*FirestoreRuleStore, error) {
	collection := prefix + "-rules"
	ctx, cancel := context.WithCancel(context.Background())

	s := &FirestoreRuleStore{
		client:     client,
		collection: collection,
		logger:     logger,
		rules:      make(map[string]*store.Rule),
		stopCh:     make(chan struct{}),
		cancel:     cancel,
	}

	// Use snapshot listener for initial load.
	snapIter := client.Collection(collection).Snapshots(ctx)

	snap, err := snapIter.Next()
	if err != nil {
		cancel()
		snapIter.Stop()
		return nil, fmt.Errorf("initial rule snapshot failed: %w", err)
	}

	now := time.Now()
	docIter := snap.Documents
	for {
		doc, err := docIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			logger.Warn("skip rule doc read error", "error", err)
			continue
		}
		var rd ruleDoc
		if err := doc.DataTo(&rd); err != nil {
			logger.Warn("skip invalid rule doc", "doc", doc.Ref.ID, "error", err)
			continue
		}
		if rd.ExpiresAt.After(now) {
			s.rules[rd.RuleID] = docToRule(rd)
		}
	}

	logger.Info("Firestore rule store loaded", "rules", len(s.rules))

	go s.listenForChanges(snapIter)
	go s.cleanupLoop()
	go s.resetRPMLoop()

	return s, nil
}

func (s *FirestoreRuleStore) SetRule(rule *store.Rule) error {
	if rule.RuleID == "" {
		return fmt.Errorf("ruleId is required")
	}
	if rule.ExpiresAt.IsZero() {
		rule.ExpiresAt = time.Now().Add(time.Duration(rule.DurationSeconds) * time.Second)
	}
	rule.ResetRPM()

	// Write to internal cache immediately.
	s.mu.Lock()
	s.rules[rule.RuleID] = rule
	s.mu.Unlock()

	// Async write to Firestore.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		doc := ruleToDoc(rule)
		_, err := s.client.Collection(s.collection).Doc(rule.RuleID).Set(ctx, doc)
		if err != nil {
			s.logger.Error("Firestore SetRule failed", "ruleId", rule.RuleID, "error", err)
		}
	}()

	return nil
}

func (s *FirestoreRuleStore) RemoveRule(ruleID string) (bool, error) {
	s.mu.Lock()
	_, found := s.rules[ruleID]
	if found {
		delete(s.rules, ruleID)
	}
	s.mu.Unlock()

	if found {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := s.client.Collection(s.collection).Doc(ruleID).Delete(ctx)
			if err != nil {
				s.logger.Error("Firestore RemoveRule failed", "ruleId", ruleID, "error", err)
			}
		}()
	}

	return found, nil
}

func (s *FirestoreRuleStore) RemoveAll() int {
	s.mu.Lock()
	count := len(s.rules)
	s.rules = make(map[string]*store.Rule)
	s.mu.Unlock()

	go func() {
		ctx := context.Background()
		bw := s.client.BulkWriter(ctx)
		iter := s.client.Collection(s.collection).Documents(ctx)
		defer iter.Stop()

		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				s.logger.Error("RemoveAll iterate error", "error", err)
				break
			}
			if _, err := bw.Delete(doc.Ref); err != nil {
				s.logger.Error("RemoveAll delete error", "error", err)
			}
		}
		bw.End()
	}()

	return count
}

func (s *FirestoreRuleStore) ShouldAllow(host, reqPath string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	for _, rule := range s.rules {
		if !rule.ExpiresAt.After(now) {
			continue
		}
		if rule.TargetHost != host {
			continue
		}
		if rule.PathPattern != nil && !matchRulePath(*rule.PathPattern, reqPath) {
			continue
		}
		switch rule.Action {
		case "block":
			return false
		case "allow":
			return true
		case "throttle":
			return rule.IncrementRPM() <= int64(rule.MaxRPM)
		}
	}
	return true
}

func (s *FirestoreRuleStore) GetStatus() []store.RuleStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	result := make([]store.RuleStatus, 0, len(s.rules))
	for _, rule := range s.rules {
		if !rule.ExpiresAt.After(now) {
			continue
		}
		result = append(result, store.RuleStatus{
			RuleID:     rule.RuleID,
			TargetHost: rule.TargetHost,
			Action:     rule.Action,
			MaxRPM:     rule.MaxRPM,
			CurrentRPM: rule.CurrentRPMValue(),
			Status:     "active",
			ExpiresAt:  rule.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	return result
}

func (s *FirestoreRuleStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		close(s.stopCh)
		s.cancel()
	}
}

func (s *FirestoreRuleStore) listenForChanges(snapIter *firestore.QuerySnapshotIterator) {
	defer snapIter.Stop()
	for {
		snap, err := snapIter.Next()
		if err != nil {
			select {
			case <-s.stopCh:
				return
			default:
				s.logger.Error("snapshot listener error", "error", err)
				return
			}
		}

		for _, change := range snap.Changes {
			switch change.Kind {
			case firestore.DocumentAdded, firestore.DocumentModified:
				var rd ruleDoc
				if err := change.Doc.DataTo(&rd); err != nil {
					s.logger.Warn("skip invalid rule change", "error", err)
					continue
				}
				rule := docToRule(rd)
				s.mu.Lock()
				s.rules[rule.RuleID] = rule
				s.mu.Unlock()
			case firestore.DocumentRemoved:
				ruleID := change.Doc.Ref.ID
				s.mu.Lock()
				delete(s.rules, ruleID)
				s.mu.Unlock()
			}
		}
	}
}

func (s *FirestoreRuleStore) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.deleteExpired()
		case <-s.stopCh:
			return
		}
	}
}

func (s *FirestoreRuleStore) deleteExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, rule := range s.rules {
		if !rule.ExpiresAt.After(now) {
			delete(s.rules, id)
		}
	}
}

func (s *FirestoreRuleStore) resetRPMLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.resetAllRPM()
		case <-s.stopCh:
			return
		}
	}
}

func (s *FirestoreRuleStore) resetAllRPM() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rule := range s.rules {
		rule.ResetRPM()
	}
}

func docToRule(d ruleDoc) *store.Rule {
	r := &store.Rule{
		RuleID:          d.RuleID,
		TargetHost:      d.TargetHost,
		Action:          d.Action,
		MaxRPM:          d.MaxRPM,
		PathPattern:     d.PathPattern,
		RateByKey:       d.RateByKey,
		Limiter:         d.Limiter,
		ThrottleDelayMs: d.ThrottleDelayMs,
		MaxDelaySlots:   d.MaxDelaySlots,
		DurationSeconds: d.DurationSeconds,
		ExpiresAt:       d.ExpiresAt,
	}
	r.ResetRPM()
	return r
}

func ruleToDoc(r *store.Rule) ruleDoc {
	now := time.Now()
	return ruleDoc{
		RuleID:          r.RuleID,
		TargetHost:      r.TargetHost,
		Action:          r.Action,
		MaxRPM:          r.MaxRPM,
		PathPattern:     r.PathPattern,
		RateByKey:       r.RateByKey,
		Limiter:         r.Limiter,
		ThrottleDelayMs: r.ThrottleDelayMs,
		MaxDelaySlots:   r.MaxDelaySlots,
		DurationSeconds: r.DurationSeconds,
		ExpiresAt:       r.ExpiresAt,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func matchRulePath(pattern, reqPath string) bool {
	if pattern == "" {
		return true
	}
	if strings.ContainsAny(pattern, "*?[") {
		matched, _ := path.Match(pattern, reqPath)
		return matched
	}
	return strings.HasPrefix(reqPath, pattern)
}
