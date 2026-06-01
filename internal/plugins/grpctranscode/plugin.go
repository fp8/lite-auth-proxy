package grpctranscode

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

// discoveryProbeThrottle bounds how often the request path will retry probing a
// not-yet-discovered backend, so a down backend under fall-through traffic isn't
// hammered with one health check per request.
const discoveryProbeThrottle = 3 * time.Second

func init() {
	plugin.Register(&grpcTranscodePlugin{})
}

type grpcTranscodePlugin struct {
	mu              sync.Mutex
	backends        []*backendConn
	router          *routeTable
	logger          *slog.Logger
	cfg             *config.GRPCConfig
	serverTargetURL string // server.target_url — the default backend when no [[grpc.backends]]

	// allDiscovered is a fast path: once every backend has been discovered, the
	// request hot path skips the bootstrap check entirely.
	allDiscovered atomic.Bool
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
	// The gRPC backend defaults to server.target_url; [[grpc.backends]] is only
	// needed for multiple backends or base_url namespacing.
	if len(cfg.GRPC.Backends) == 0 {
		if strings.TrimSpace(cfg.Server.TargetURL) == "" {
			return fmt.Errorf("grpc.enabled is true but neither server.target_url nor [[grpc.backends]] is set")
		}
		return nil
	}

	// When explicit backends are given, server.target_url is still required core
	// config — so it must be meaningful: its address must be one of the backends.
	// Otherwise target_url could point somewhere unrelated (or a dead port) while
	// traffic goes to the backends, which is silently confusing.
	target, _, err := grpcAddressFromTarget(cfg.Server.TargetURL)
	if err != nil {
		return fmt.Errorf("grpc.enabled with [[grpc.backends]] requires a valid server.target_url: %w", err)
	}
	addrs := make([]string, len(cfg.GRPC.Backends))
	for i, b := range cfg.GRPC.Backends {
		addrs[i] = b.Address
		if b.Address == target {
			return nil
		}
	}
	return fmt.Errorf("server.target_url %q resolves to %q, which must also be one of the [[grpc.backends]] addresses (configured: %s)",
		cfg.Server.TargetURL, target, strings.Join(addrs, ", "))
}

// Start dials each backend. It does NOT probe or discover anything here, and
// never fails because a backend is unreachable: in a sidecar the gRPC service
// may take seconds to come up while the proxy boots in milliseconds. Readiness
// is established on demand by the health endpoint (see Ready) — the proxy always
// boots and serves /healthz immediately; the orchestrator's startup probe is
// what gates traffic and decides whether to keep the container.
func (p *grpcTranscodePlugin) Start(_ context.Context) error {
	if p.cfg == nil || !p.cfg.Enabled {
		return nil
	}

	// Resolve which gRPC backend(s) to dial. Explicit [[grpc.backends]] take
	// precedence (multiple backends, base_url namespacing); otherwise the single
	// backend is derived from server.target_url — so the common case needs only
	// grpc.enabled=true plus the target the proxy is already configured with.
	type target struct {
		cfg config.GRPCBackend
		tls bool
	}
	var targets []target
	if len(p.cfg.Backends) > 0 {
		for _, b := range p.cfg.Backends {
			targets = append(targets, target{cfg: b, tls: p.cfg.UpstreamTLS})
		}
	} else {
		addr, tlsFromScheme, err := grpcAddressFromTarget(p.serverTargetURL)
		if err != nil {
			// No address source at all — a static misconfiguration; fail boot.
			return fmt.Errorf("grpc: no [[grpc.backends]] and server.target_url is unusable as a gRPC address: %w", err)
		}
		targets = append(targets, target{
			cfg: config.GRPCBackend{Address: addr},
			tls: p.cfg.UpstreamTLS || tlsFromScheme,
		})
	}

	// Start may run more than once on the same plugin instance (the registry is
	// a singleton — notably across tests); reset so a fresh run doesn't keep
	// stale backends.
	p.allDiscovered.Store(false)
	p.mu.Lock()
	for _, bc := range p.backends {
		_ = bc.close()
	}
	p.backends = nil
	p.mu.Unlock()

	for _, t := range targets {
		bc, err := dialBackend(context.Background(), t.cfg, t.tls)
		if err != nil {
			// grpc.NewClient only errors on a malformed target. Record it; the
			// health endpoint reports this backend unavailable. conn stays nil.
			bc = &backendConn{address: t.cfg.Address, baseURL: t.cfg.BaseURL}
			bc.setError(fmt.Errorf("dial: %w", err))
			p.logger.Error("grpc backend dial failed", "address", t.cfg.Address, "error", err)
		} else {
			bc.setError(errors.New("waiting: health check not run yet"))
		}
		p.mu.Lock()
		p.backends = append(p.backends, bc)
		p.mu.Unlock()
	}

	p.logger.Info("grpc transcoding started; readiness is gated by the health check",
		"backends", len(p.backends))
	return nil
}

// grpcAddressFromTarget derives a gRPC dial address (host:port) from
// server.target_url. The URL scheme only informs TLS (https → TLS); the gRPC
// connection uses host:port. target_url is expected to be scheme://host:port,
// as the HTTP reverse proxy already requires.
func grpcAddressFromTarget(target string) (addr string, tls bool, err error) {
	if strings.TrimSpace(target) == "" {
		return "", false, fmt.Errorf("server.target_url is empty")
	}
	u, perr := url.Parse(target)
	if perr != nil {
		return "", false, fmt.Errorf("parse %q: %w", target, perr)
	}
	if u.Host == "" {
		return "", false, fmt.Errorf("%q has no host:port (expected scheme://host:port)", target)
	}
	return u.Host, strings.EqualFold(u.Scheme, "https"), nil
}

func (p *grpcTranscodePlugin) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, bc := range p.backends {
		_ = bc.close()
	}
	p.backends = nil
	return nil
}

// Ready is invoked on every /healthz call. It probes every backend CONCURRENTLY
// with a LIVE gRPC health check (translating the proxy's health probe into a
// backend health probe) and, the first time a backend reports SERVING, discovers
// its services via reflection and installs the routes. It returns nil when every
// backend is ready, or an error describing the problem for each failing backend:
//
//   - backend not reachable / NOT_SERVING        → "waiting: ..."  (still starting)
//   - health or reflection service absent        → "unavailable: ..." (Unimplemented)
//
// Both map to HTTP 503 at /healthz (the startup probe keeps waiting); only an
// all-ready result yields 200. The cached per-backend state it records is also
// what the request path uses to short-circuit calls to a not-ready backend.
func (p *grpcTranscodePlugin) Ready() error {
	if p.cfg == nil || !p.cfg.Enabled {
		return nil
	}
	p.mu.Lock()
	backends := append([]*backendConn(nil), p.backends...)
	p.mu.Unlock()
	if len(backends) == 0 {
		return nil
	}

	// Probe all backends concurrently (force: /healthz is always a live check),
	// so one slow or unreachable backend doesn't serialize the others — each
	// probe carries its own timeout. Results are kept per index for a stable,
	// deterministic error message.
	errs := make([]error, len(backends))
	var wg sync.WaitGroup
	wg.Add(len(backends))
	for i, bc := range backends {
		go func(i int, bc *backendConn) {
			defer wg.Done()
			errs[i] = p.probe(bc, true)
		}(i, bc)
	}
	wg.Wait()

	var problems []string
	for _, err := range errs {
		if err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("gRPC not ready: %s", strings.Join(problems, "; "))
	}
	return nil
}

// ensureDiscovered bootstraps discovery from the REQUEST path, so the
// transcoding endpoint works even when nothing ever calls /healthz (e.g. no
// startup probe configured). It probes any not-yet-discovered backend, throttled
// per backend, and becomes a no-op once every backend is discovered.
func (p *grpcTranscodePlugin) ensureDiscovered() {
	if p.allDiscovered.Load() {
		return
	}
	p.mu.Lock()
	backends := append([]*backendConn(nil), p.backends...)
	p.mu.Unlock()
	if len(backends) == 0 {
		return
	}

	pending := false
	for _, bc := range backends {
		if bc.isDiscovered() {
			continue
		}
		p.probe(bc, false) // throttled; safe to call on every request
		if !bc.isDiscovered() {
			pending = true
		}
	}
	if !pending {
		p.allDiscovered.Store(true)
	}
}

// probe runs probeAndDiscover for a backend. When force is false it is throttled
// (discoveryProbeThrottle) and de-duplicated so concurrent requests don't all
// probe; /healthz passes force=true to always perform a live check.
func (p *grpcTranscodePlugin) probe(bc *backendConn, force bool) error {
	if !force {
		bc.probeMu.Lock()
		if !bc.lastProbeAt.IsZero() && time.Since(bc.lastProbeAt) < discoveryProbeThrottle {
			bc.probeMu.Unlock()
			_, err := bc.status()
			return err
		}
		bc.lastProbeAt = time.Now()
		bc.probeMu.Unlock()
	} else {
		bc.probeMu.Lock()
		bc.lastProbeAt = time.Now()
		bc.probeMu.Unlock()
	}
	return p.probeAndDiscover(bc)
}

// probeAndDiscover performs the live health check for one backend and, on the
// first SERVING result, discovers and installs its routes (once). It updates the
// backend's cached readiness state and returns nil when ready.
func (p *grpcTranscodePlugin) probeAndDiscover(bc *backendConn) error {
	if bc.conn == nil {
		_, lastErr := bc.status()
		return fmt.Errorf("unavailable: backend %s: %v", bc.address, lastErr)
	}

	// Live health check — this is the proxy's health probe translated into a
	// gRPC health probe on the backend.
	healthCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	herr := bc.checkHealth(healthCtx)
	cancel()
	if herr != nil {
		if grpcCode(herr) == codes.Unimplemented {
			err := fmt.Errorf("unavailable: backend %s: health service not enabled — %w", bc.address, herr)
			bc.setError(err)
			return err
		}
		// Connection refused / UNAVAILABLE / DEADLINE_EXCEEDED / NOT_SERVING:
		// the backend is not up (or not serving) yet.
		err := fmt.Errorf("waiting: backend %s: not reachable or not serving — %w", bc.address, herr)
		bc.setError(err)
		return err
	}

	// Health is SERVING. Discover services exactly once (cached forever).
	bc.discoverMu.Lock()
	defer bc.discoverMu.Unlock()
	if !bc.discovered {
		reflCtx, reflCancel := context.WithTimeout(context.Background(), 5*time.Second)
		methods, rerr := discoverServices(reflCtx, bc.conn)
		reflCancel()
		if rerr != nil {
			if grpcCode(rerr) == codes.Unimplemented {
				err := fmt.Errorf("unavailable: backend %s: reflection not enabled — %w", bc.address, rerr)
				bc.setError(err)
				return err
			}
			err := fmt.Errorf("waiting: backend %s: reflection failed — %w", bc.address, rerr)
			bc.setError(err)
			return err
		}
		p.router.setBackendRoutes(bc, buildRoutes(p.cfg.RouteMode, p.logger, methods, bc))
		bc.discovered = true
		p.logger.Info("grpc backend discovered",
			"address", bc.address, "base_url", bc.baseURL,
			"methods", len(methods), "total_routes", p.router.routeCount())
	}

	bc.setReady()
	return nil
}

// grpcCode extracts the gRPC status code from err (unwrapping wrapped errors),
// or codes.Unknown if it carries none.
func grpcCode(err error) codes.Code {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if se, ok := e.(interface{ GRPCStatus() *status.Status }); ok {
			return se.GRPCStatus().Code()
		}
	}
	return codes.Unknown
}

func (p *grpcTranscodePlugin) BuildMiddleware(deps *plugin.Deps) ([]plugin.Middleware, error) {
	if !deps.Config.GRPC.Enabled {
		return nil, nil
	}

	p.logger = deps.Logger.With("plugin", "grpctranscode")
	p.cfg = &deps.Config.GRPC
	p.serverTargetURL = deps.Config.Server.TargetURL
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

	// This middleware is only built when gRPC transcoding is enabled, so the
	// backend is a gRPC service: there is NO HTTP fall-through. Every non-health
	// request is therefore owned here — it is either transcoded to gRPC, or
	// rejected (404 unknown method / 503 not ready). `next` is intentionally not
	// called.
	mw := func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Bootstrap discovery from the request path so the endpoint works
			// even if nothing ever calls /healthz. No-op once all backends are
			// discovered; throttled while any are still pending.
			p.ensureDiscovered()

			entry, pathVars := p.router.match(r.Method, r.URL.Path)
			if entry == nil {
				// No matching gRPC method. While discovery is still pending the
				// route set isn't fully known and the backend isn't ready, so
				// report 503; once everything is discovered an unmatched path is
				// genuinely not a gRPC method, so report 404.
				if !p.allDiscovered.Load() {
					problemJSON(w, http.StatusServiceUnavailable, "Service Unavailable", "gRPC backend not ready")
					return
				}
				problemJSON(w, http.StatusNotFound, "Not Found",
					"no gRPC method matches "+r.Method+" "+r.URL.Path)
				return
			}
			// The route exists (backend was discovered). If its last health probe
			// found it not-ready, short-circuit rather than dialling a dead backend.
			if ready, lastErr := entry.backend.status(); !ready {
				detail := "gRPC backend not ready"
				if lastErr != nil {
					detail = lastErr.Error()
				}
				problemJSON(w, http.StatusServiceUnavailable, "Service Unavailable", detail)
				return
			}
			transcodeRequest(w, r, entry, pathVars, headerPrefix, forwardAuth, timeout, marshalOpts, unmarshalOpts, logger)
		})
	}

	return []plugin.Middleware{mw}, nil
}

// buildRoutes converts discovered methods into route entries based on route_mode.
func buildRoutes(mode string, logger *slog.Logger, methods []discoveredMethod, bc *backendConn) []routeEntry {
	var entries []routeEntry

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
					logger.Warn("skipping invalid annotation route",
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
