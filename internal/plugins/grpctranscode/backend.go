package grpctranscode

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// backendConn represents a gRPC connection to a single upstream backend.
//
// Readiness (ready/lastErr) is tracked separately from the connection because
// the backend may not be reachable when the proxy boots — discovery runs in the
// background and updates this state, which the proxy's /healthz surfaces. The
// state is guarded by stateMu for concurrent reads from the health handler.
type backendConn struct {
	address string
	baseURL string
	conn    *grpc.ClientConn

	// discovered (guarded by discoverMu) records that reflection has run and the
	// backend's routes are installed. Discovery happens once, on the first
	// SERVING health check, and is cached for the process lifetime.
	discoverMu sync.Mutex
	discovered bool

	// lastProbeAt (guarded by probeMu) throttles and de-duplicates request-path
	// probes so a not-yet-discovered backend isn't probed on every request.
	probeMu     sync.Mutex
	lastProbeAt time.Time

	// ready/lastErr (guarded by stateMu) is the last-known readiness, updated by
	// each probe and read by the request path to short-circuit calls to a
	// not-ready backend.
	stateMu sync.RWMutex
	ready   bool
	lastErr error
}

// isDiscovered reports whether the backend's routes have been installed.
func (b *backendConn) isDiscovered() bool {
	b.discoverMu.Lock()
	defer b.discoverMu.Unlock()
	return b.discovered
}

// setReady marks the backend as ready and clears any recorded error.
func (b *backendConn) setReady() {
	b.stateMu.Lock()
	b.ready = true
	b.lastErr = nil
	b.stateMu.Unlock()
}

// setError records why the backend is not (yet) usable and marks it not ready.
func (b *backendConn) setError(err error) {
	b.stateMu.Lock()
	b.ready = false
	b.lastErr = err
	b.stateMu.Unlock()
}

// status returns the current readiness and last error.
func (b *backendConn) status() (bool, error) {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.ready, b.lastErr
}

// dialBackend establishes a gRPC connection to a backend.
func dialBackend(ctx context.Context, cfg config.GRPCBackend, upstreamTLS bool) (*backendConn, error) {
	var opts []grpc.DialOption
	if !upstreamTLS {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	conn, err := grpc.NewClient(cfg.Address, opts...)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", cfg.Address, err)
	}
	return &backendConn{
		address: cfg.Address,
		baseURL: cfg.BaseURL,
		conn:    conn,
	}, nil
}

// checkHealth calls grpc.health.v1.Health/Check on the backend.
func (b *backendConn) checkHealth(ctx context.Context) error {
	client := healthpb.NewHealthClient(b.conn)
	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("health check on %s: %w", b.address, err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("health check on %s: status %s", b.address, resp.Status)
	}
	return nil
}

// close shuts down the gRPC connection.
func (b *backendConn) close() error {
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}
