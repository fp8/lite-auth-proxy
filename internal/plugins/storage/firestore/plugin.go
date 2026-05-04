package firestore

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"

	"cloud.google.com/go/firestore"
	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/plugin"
	"github.com/fp8/lite-auth-proxy/internal/store"
)

func init() {
	plugin.Register(&Plugin{})
}

// Plugin implements the Firestore storage backend.
type Plugin struct {
	client *firestore.Client
	prefix string
	logger *slog.Logger
}

func (p *Plugin) Name() string  { return "storage-firestore" }
func (p *Plugin) Priority() int { return 5 }

func (p *Plugin) ValidateConfig(cfg *config.Config) error {
	if !cfg.Storage.Enabled {
		return nil
	}
	projectID := resolveProjectID(cfg)
	if projectID == "" {
		return fmt.Errorf("storage.project_id is required when backend is firestore (set GOOGLE_CLOUD_PROJECT or storage.project_id)")
	}
	prefix := cfg.Storage.CollectionPrefix
	if prefix == "" {
		prefix = "proxy"
	}
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(prefix) {
		return fmt.Errorf("storage.collection_prefix must contain only [a-z0-9-], got: %q", prefix)
	}
	return nil
}

func (p *Plugin) Open(cfg *config.Config, logger *slog.Logger) error {
	projectID := resolveProjectID(cfg)
	prefix := cfg.Storage.CollectionPrefix
	if prefix == "" {
		prefix = "proxy"
	}

	ctx := context.Background()
	dbname := cfg.Storage.Dbname
	var client *firestore.Client
	var err error
	if dbname != "" {
		client, err = firestore.NewClientWithDatabase(ctx, projectID, dbname)
	} else {
		client, err = firestore.NewClient(ctx, projectID)
	}
	if err != nil {
		return fmt.Errorf("failed to create Firestore client: %w", err)
	}

	// Validate connection with a lightweight read.
	iter := client.Collection(prefix + "-rules").Limit(1).Documents(ctx)
	_, _ = iter.Next()
	iter.Stop()

	p.client = client
	p.prefix = prefix
	p.logger = logger

	logArgs := []any{"project", projectID, "prefix", prefix}
	if dbname != "" {
		logArgs = append(logArgs, "database", dbname)
	}
	logger.Info("Firestore storage connected", logArgs...)
	return nil
}

func (p *Plugin) NewRuleStore(cfg *config.Config, logger *slog.Logger) (store.RuleStore, error) {
	return NewFirestoreRuleStore(p.client, p.prefix, logger)
}

func (p *Plugin) NewKeyValueStore(namespace string) (store.KeyValueStore, error) {
	return NewFirestoreKeyValueStore(p.client, p.prefix, namespace), nil
}

func (p *Plugin) Start(_ context.Context) error { return nil }

func (p *Plugin) Stop() error {
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

func resolveProjectID(cfg *config.Config) string {
	if cfg.Storage.ProjectID != "" {
		return cfg.Storage.ProjectID
	}
	if v := os.Getenv("GOOGLE_CLOUD_PROJECT"); v != "" {
		return v
	}
	return config.FetchGCPProjectID()
}
