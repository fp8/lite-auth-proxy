package grpctranscode

import (
	"context"
	"fmt"

	"github.com/fp8/lite-auth-proxy/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// backendConn represents a gRPC connection to a single upstream backend.
type backendConn struct {
	address string
	baseURL string
	conn    *grpc.ClientConn
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
