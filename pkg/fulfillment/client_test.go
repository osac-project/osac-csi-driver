package fulfillment

import (
	"context"
	"net"
	"testing"

	privatev1 "github.com/osac-project/osac-csi-driver/internal/api/osac/private/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// fakeTiersServer implements StorageTiersServer with canned responses.
type fakeTiersServer struct {
	privatev1.UnimplementedStorageTiersServer
	listResp *privatev1.StorageTiersListResponse
	listErr  error
}

func (f *fakeTiersServer) List(_ context.Context, _ *privatev1.StorageTiersListRequest) (*privatev1.StorageTiersListResponse, error) {
	return f.listResp, f.listErr
}

// fakeBackendsServer implements StorageBackendsServer with canned responses.
type fakeBackendsServer struct {
	privatev1.UnimplementedStorageBackendsServer
	getResp *privatev1.StorageBackendsGetResponse
	getErr  error
}

func (f *fakeBackendsServer) Get(_ context.Context, _ *privatev1.StorageBackendsGetRequest) (*privatev1.StorageBackendsGetResponse, error) {
	return f.getResp, f.getErr
}

func startFakeServer(t *testing.T, tiers *fakeTiersServer, backends *fakeBackendsServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	privatev1.RegisterStorageTiersServer(srv, tiers)
	privatev1.RegisterStorageBackendsServer(srv, backends)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)
	return lis.Addr().String()
}

func newTestClient(t *testing.T, addr string, vendorSockets map[string]string) *GRPCClient {
	t.Helper()
	c, err := NewGRPCClient(addr, vendorSockets,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestResolve_HappyPath(t *testing.T) {
	tiers := &fakeTiersServer{
		listResp: &privatev1.StorageTiersListResponse{
			Size:  1,
			Total: 1,
			Items: []*privatev1.StorageTier{{
				Id:       "tier-1",
				Metadata: &privatev1.Metadata{Name: "gold"},
				Spec: &privatev1.StorageTierSpec{
					Backends: []*privatev1.BackendAssociation{{
						BackendId: "backend-1",
						Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					}},
				},
			}},
		},
	}
	backends := &fakeBackendsServer{
		getResp: &privatev1.StorageBackendsGetResponse{
			Object: &privatev1.StorageBackend{
				Id: "backend-1",
				Spec: &privatev1.StorageBackendSpec{
					Provider: "vast",
					Endpoint: "https://vast-mgmt.example.com",
				},
			},
		},
	}

	addr := startFakeServer(t, tiers, backends)
	c := newTestClient(t, addr, map[string]string{"vast": "/csi/vast/csi.sock"})

	result, err := c.Resolve(context.Background(), "acme", "gold")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Backend != "vast" {
		t.Errorf("Backend = %q, want %q", result.Backend, "vast")
	}
	if result.Endpoint != "/csi/vast/csi.sock" {
		t.Errorf("Endpoint = %q, want %q", result.Endpoint, "/csi/vast/csi.sock")
	}
	if result.Protocol != "block" {
		t.Errorf("Protocol = %q, want %q", result.Protocol, "block")
	}
}

func TestResolve_NFS_Protocol(t *testing.T) {
	tiers := &fakeTiersServer{
		listResp: &privatev1.StorageTiersListResponse{
			Size: 1, Total: 1,
			Items: []*privatev1.StorageTier{{
				Id:       "tier-2",
				Metadata: &privatev1.Metadata{Name: "silver"},
				Spec: &privatev1.StorageTierSpec{
					Backends: []*privatev1.BackendAssociation{{
						BackendId: "backend-2",
						Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
					}},
				},
			}},
		},
	}
	backends := &fakeBackendsServer{
		getResp: &privatev1.StorageBackendsGetResponse{
			Object: &privatev1.StorageBackend{
				Id:   "backend-2",
				Spec: &privatev1.StorageBackendSpec{Provider: "vast"},
			},
		},
	}

	addr := startFakeServer(t, tiers, backends)
	c := newTestClient(t, addr, map[string]string{"vast": "/csi/vast/csi.sock"})

	result, err := c.Resolve(context.Background(), "acme", "silver")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Protocol != "nfs" {
		t.Errorf("Protocol = %q, want %q", result.Protocol, "nfs")
	}
}

func TestResolve_TierNotFound(t *testing.T) {
	tiers := &fakeTiersServer{
		listResp: &privatev1.StorageTiersListResponse{
			Size: 0, Total: 0, Items: nil,
		},
	}
	backends := &fakeBackendsServer{}

	addr := startFakeServer(t, tiers, backends)
	c := newTestClient(t, addr, map[string]string{"vast": "/csi/vast/csi.sock"})

	_, err := c.Resolve(context.Background(), "acme", "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing tier")
	}
	if got := err.Error(); got != `storage tier "nonexistent" not found` {
		t.Errorf("error = %q, want storage tier not found message", got)
	}
}

func TestResolve_TierListError(t *testing.T) {
	tiers := &fakeTiersServer{
		listErr: status.Error(codes.Unavailable, "server down"),
	}
	backends := &fakeBackendsServer{}

	addr := startFakeServer(t, tiers, backends)
	c := newTestClient(t, addr, map[string]string{})

	_, err := c.Resolve(context.Background(), "acme", "gold")
	if err == nil {
		t.Fatal("expected error when tier list fails")
	}
}

func TestResolve_BackendNotFound(t *testing.T) {
	tiers := &fakeTiersServer{
		listResp: &privatev1.StorageTiersListResponse{
			Size: 1, Total: 1,
			Items: []*privatev1.StorageTier{{
				Id:       "tier-1",
				Metadata: &privatev1.Metadata{Name: "gold"},
				Spec: &privatev1.StorageTierSpec{
					Backends: []*privatev1.BackendAssociation{{
						BackendId: "backend-missing",
						Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					}},
				},
			}},
		},
	}
	backends := &fakeBackendsServer{
		getErr: status.Error(codes.NotFound, "backend not found"),
	}

	addr := startFakeServer(t, tiers, backends)
	c := newTestClient(t, addr, map[string]string{"vast": "/csi/vast/csi.sock"})

	_, err := c.Resolve(context.Background(), "acme", "gold")
	if err == nil {
		t.Fatal("expected error when backend not found")
	}
}

func TestResolve_NoVendorSocket(t *testing.T) {
	tiers := &fakeTiersServer{
		listResp: &privatev1.StorageTiersListResponse{
			Size: 1, Total: 1,
			Items: []*privatev1.StorageTier{{
				Id:       "tier-1",
				Metadata: &privatev1.Metadata{Name: "gold"},
				Spec: &privatev1.StorageTierSpec{
					Backends: []*privatev1.BackendAssociation{{
						BackendId: "backend-1",
						Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					}},
				},
			}},
		},
	}
	backends := &fakeBackendsServer{
		getResp: &privatev1.StorageBackendsGetResponse{
			Object: &privatev1.StorageBackend{
				Id:   "backend-1",
				Spec: &privatev1.StorageBackendSpec{Provider: "unknown-vendor"},
			},
		},
	}

	addr := startFakeServer(t, tiers, backends)
	c := newTestClient(t, addr, map[string]string{"vast": "/csi/vast/csi.sock"})

	_, err := c.Resolve(context.Background(), "acme", "gold")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if got := err.Error(); got != `no vendor socket configured for provider "unknown-vendor"` {
		t.Errorf("error = %q, want provider not found message", got)
	}
}

func TestResolve_NoBackendAssociations(t *testing.T) {
	tiers := &fakeTiersServer{
		listResp: &privatev1.StorageTiersListResponse{
			Size: 1, Total: 1,
			Items: []*privatev1.StorageTier{{
				Id:       "tier-1",
				Metadata: &privatev1.Metadata{Name: "gold"},
				Spec:     &privatev1.StorageTierSpec{Backends: nil},
			}},
		},
	}
	backends := &fakeBackendsServer{}

	addr := startFakeServer(t, tiers, backends)
	c := newTestClient(t, addr, map[string]string{})

	_, err := c.Resolve(context.Background(), "acme", "gold")
	if err == nil {
		t.Fatal("expected error for tier with no backends")
	}
}

func TestClose(t *testing.T) {
	tiers := &fakeTiersServer{}
	backends := &fakeBackendsServer{}
	addr := startFakeServer(t, tiers, backends)

	c, err := NewGRPCClient(addr, nil,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestProtocolToString(t *testing.T) {
	tests := []struct {
		input privatev1.StorageProtocol
		want  string
	}{
		{privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK, "block"},
		{privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS, "nfs"},
		{privatev1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED, "unspecified"},
	}
	for _, tt := range tests {
		got := protocolToString(tt.input)
		if got != tt.want {
			t.Errorf("protocolToString(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
