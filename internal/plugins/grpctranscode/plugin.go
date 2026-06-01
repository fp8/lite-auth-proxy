package grpctranscode

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/plugin"
	"google.golang.org/protobuf/encoding/protojson"
)

func init() {
	plugin.Register(&grpcTranscodePlugin{})
}

type grpcTranscodePlugin struct {
	mu       sync.Mutex
	backends []*backendConn
	router   *routeTable
	logger   *slog.Logger
	cfg      *config.GRPCConfig
	cancel   context.CancelFunc
}

func (p *grpcTranscodePlugin) Name() string  { return "grpctranscode" }
func (p *grpcTranscodePlugin) Priority() int { return 95 }

func (p *grpcTranscodePlugin) ValidateConfig(cfg *config.Config) error {
	if !cfg.GRPC.Enabled {
		return nil
	}
	switch cfg.GRPC.RouteMode {
	case "annotation", "convention", "auto":
	default:
		return fmt.Errorf("grpc.route_mode must be \"annotation\", \"convention\", or \"auto\", got: %q", cfg.GRPC.RouteMode)
	}
	return nil
}

func (p *grpcTranscodePlugin) Start(ctx context.Context) error {
	if p.cfg == nil || !p.cfg.Enabled {
		return nil
	}

	// Determine backend list. If none configured, use the primary target.
	backends := p.cfg.Backends
	if len(backends) == 0 {
		return fmt.Errorf("grpc: no backends configured and no primary target to reflect")
	}

	for _, bcfg := range backends {
		bc, err := dialBackend(ctx, bcfg, p.cfg.UpstreamTLS)
		if err != nil {
			return fmt.Errorf("grpc: %w", err)
		}

		// Verify health check.
		healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
		err = bc.checkHealth(healthCtx)
		healthCancel()
		if err != nil {
			_ = bc.close()
			return fmt.Errorf("grpc: backend %s: health check service is absent or unhealthy — %w", bcfg.Address, err)
		}

		// Discover services via reflection.
		reflCtx, reflCancel := context.WithTimeout(ctx, 10*time.Second)
		methods, err := discoverServices(reflCtx, bc.conn)
		reflCancel()
		if err != nil {
			_ = bc.close()
			return fmt.Errorf("grpc: backend %s: reflection is absent or failed — %w", bcfg.Address, err)
		}

		// Build routes.
		entries := p.buildRoutes(methods, bc)
		p.router.mu.Lock()
		p.router.entries = append(p.router.entries, entries...)
		p.router.mu.Unlock()

		p.mu.Lock()
		p.backends = append(p.backends, bc)
		p.mu.Unlock()

		p.logger.Info("grpc backend ready",
			"address", bcfg.Address,
			"base_url", bcfg.BaseURL,
			"routes", len(entries),
			"methods", len(methods),
		)
	}

	totalRoutes := p.router.routeCount()
	p.logger.Info("grpc transcoding started", "total_routes", totalRoutes, "backends", len(p.backends))

	// Start periodic refresh if configured.
	if p.cfg.Reflection && p.cfg.ReflectionRefreshS > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		p.cancel = cancel
		go p.refreshLoop(ctx)
	}

	return nil
}

func (p *grpcTranscodePlugin) Stop() error {
	if p.cancel != nil {
		p.cancel()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, bc := range p.backends {
		_ = bc.close()
	}
	p.backends = nil
	return nil
}

func (p *grpcTranscodePlugin) BuildMiddleware(deps *plugin.Deps) ([]plugin.Middleware, error) {
	if !deps.Config.GRPC.Enabled {
		return nil, nil
	}

	p.logger = deps.Logger.With("plugin", "grpctranscode")
	p.cfg = &deps.Config.GRPC
	p.router = newRouteTable()

	headerPrefix := deps.Config.Auth.HeaderPrefix
	forwardAuth := deps.Config.GRPC.ForwardAuthHeaders
	timeout := time.Duration(deps.Config.GRPC.RequestTimeoutSecs) * time.Second

	marshalOpts := protojson.MarshalOptions{
		EmitUnpopulated: deps.Config.GRPC.EmitUnpopulated,
		UseProtoNames:   deps.Config.GRPC.UseProtoNames,
	}
	unmarshalOpts := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}

	logger := p.logger

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entry, pathVars := p.router.match(r.Method, r.URL.Path)
			if entry == nil {
				next.ServeHTTP(w, r)
				return
			}
			transcodeRequest(w, r, entry, pathVars, headerPrefix, forwardAuth, timeout, marshalOpts, unmarshalOpts, logger)
		})
	}

	return []plugin.Middleware{mw}, nil
}

// buildRoutes converts discovered methods into route entries based on route_mode.
func (p *grpcTranscodePlugin) buildRoutes(methods []discoveredMethod, bc *backendConn) []routeEntry {
	var entries []routeEntry
	mode := p.cfg.RouteMode

	for _, m := range methods {
		var added bool

		// Annotation mode: try to parse google.api.http.
		if mode == "annotation" || mode == "auto" {
			binding := parseHTTPAnnotation(m.methodDesc)
			if binding != nil {
				entry, err := buildAnnotationRoute(
					bc.baseURL, binding.method, binding.path, binding.body,
					m.fullMethod, m.inputDesc, m.outputDesc, bc,
				)
				if err != nil {
					p.logger.Warn("skipping invalid annotation route",
						"method", m.fullMethod, "error", err)
				} else {
					entries = append(entries, entry)
					added = true
				}
			}
		}

		// Convention mode: POST /<pkg>.<Service>/<Method>
		if mode == "convention" || (mode == "auto" && !added) {
			entries = append(entries, buildConventionRoute(
				bc.baseURL, m.fullMethod, m.inputDesc, m.outputDesc, bc,
			))
		}
	}
	return entries
}

// refreshLoop periodically re-discovers services from all backends.
func (p *grpcTranscodePlugin) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(p.cfg.ReflectionRefreshS) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.refresh(ctx)
		}
	}
}

// refresh re-discovers services and rebuilds the route table.
func (p *grpcTranscodePlugin) refresh(ctx context.Context) {
	p.mu.Lock()
	backends := make([]*backendConn, len(p.backends))
	copy(backends, p.backends)
	p.mu.Unlock()

	var allEntries []routeEntry
	for _, bc := range backends {
		reflCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		methods, err := discoverServices(reflCtx, bc.conn)
		cancel()
		if err != nil {
			p.logger.Warn("reflection refresh failed", "address", bc.address, "error", err)
			continue
		}
		allEntries = append(allEntries, p.buildRoutes(methods, bc)...)
	}

	p.router.setRoutes(allEntries)
	p.logger.Info("grpc routes refreshed", "total_routes", len(allEntries))
}
