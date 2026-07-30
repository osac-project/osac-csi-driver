// Package fulfillment provides the client interface for the OSAC fulfillment service.
package fulfillment

import (
	"context"
	"fmt"
	"strings"

	privatev1 "github.com/osac-project/osac-csi-driver/internal/api/osac/private/v1"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

// ResolveResult contains the resolved storage backend details for a tenant's
// storage tier. Returned by Client.Resolve after the fulfillment-service
// determines which vendor CSI driver should handle the request.
type ResolveResult struct {
	Backend  string
	Endpoint string
	Protocol string
	Params   map[string]string
}

// Client defines the interface for communicating with the OSAC fulfillment
// service's Volume API. The CSI driver calls Resolve during CreateVolume to
// determine which vendor backend to route to.
type Client interface {
	Resolve(ctx context.Context, tenant, tier string) (*ResolveResult, error)
	Close() error
}

// LoggingStub is a Client that logs calls and returns a configurable default
// backend. Used during development before the real gRPC client is available.
type LoggingStub struct {
	DefaultBackend  string
	DefaultEndpoint string
	DefaultProtocol string
}

func (s *LoggingStub) Resolve(_ context.Context, tenant, tier string) (*ResolveResult, error) {
	klog.Infof("fulfillment stub: Resolve(tenant=%q, tier=%q) -> backend=%q", tenant, tier, s.DefaultBackend)
	return &ResolveResult{
		Backend:  s.DefaultBackend,
		Endpoint: s.DefaultEndpoint,
		Protocol: s.DefaultProtocol,
	}, nil
}

func (s *LoggingStub) Close() error { return nil }

// GRPCClient is a Client that connects to the fulfillment-service's
// StorageTiers and StorageBackends gRPC APIs to resolve tier-to-backend
// routing information.
type GRPCClient struct {
	conn          *grpc.ClientConn
	tiers         privatev1.StorageTiersClient
	backends      privatev1.StorageBackendsClient
	vendorSockets map[string]string
}

// NewGRPCClient creates a fulfillment client connected to the given endpoint.
// The vendorSockets map translates backend provider names (e.g. "vast") to
// local CSI socket paths. Additional grpc.DialOptions can configure TLS and
// authentication.
func NewGRPCClient(endpoint string, vendorSockets map[string]string, opts ...grpc.DialOption) (*GRPCClient, error) {
	conn, err := grpc.NewClient(endpoint, opts...)
	if err != nil {
		return nil, fmt.Errorf("dialing fulfillment-service at %s: %w", endpoint, err)
	}
	return &GRPCClient{
		conn:          conn,
		tiers:         privatev1.NewStorageTiersClient(conn),
		backends:      privatev1.NewStorageBackendsClient(conn),
		vendorSockets: vendorSockets,
	}, nil
}

// Resolve looks up the storage tier by name via the fulfillment-service,
// fetches the associated backend, and returns the vendor routing info
// needed by the CSI controller.
func (c *GRPCClient) Resolve(ctx context.Context, tenant, tier string) (*ResolveResult, error) {
	filter := fmt.Sprintf("this.metadata.name == %q", tier)
	listResp, err := c.tiers.List(ctx, &privatev1.StorageTiersListRequest{
		Filter: &filter,
	})
	if err != nil {
		return nil, fmt.Errorf("listing storage tiers: %w", err)
	}
	if len(listResp.GetItems()) == 0 {
		return nil, fmt.Errorf("storage tier %q not found", tier)
	}

	storageTier := listResp.GetItems()[0]
	assocs := storageTier.GetSpec().GetBackends()
	if len(assocs) == 0 {
		return nil, fmt.Errorf("storage tier %q has no backend associations", tier)
	}

	backendID := assocs[0].GetBackendId()
	protocol := protocolToString(assocs[0].GetProtocol())

	getResp, err := c.backends.Get(ctx, &privatev1.StorageBackendsGetRequest{
		Id: backendID,
	})
	if err != nil {
		return nil, fmt.Errorf("getting storage backend %q: %w", backendID, err)
	}

	provider := getResp.GetObject().GetSpec().GetProvider()
	socketPath, ok := c.vendorSockets[provider]
	if !ok {
		return nil, fmt.Errorf("no vendor socket configured for provider %q", provider)
	}

	klog.Infof("fulfillment: Resolve(tenant=%q, tier=%q) -> backend=%q endpoint=%q protocol=%s",
		tenant, tier, provider, socketPath, protocol)

	return &ResolveResult{
		Backend:  provider,
		Endpoint: socketPath,
		Protocol: protocol,
	}, nil
}

func (c *GRPCClient) Close() error {
	return c.conn.Close()
}

func protocolToString(p privatev1.StorageProtocol) string {
	s := p.String()
	s = strings.TrimPrefix(s, "STORAGE_PROTOCOL_")
	return strings.ToLower(s)
}
