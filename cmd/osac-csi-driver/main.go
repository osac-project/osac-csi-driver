package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/osac-project/osac-csi-driver/pkg/driver"
	"github.com/osac-project/osac-csi-driver/pkg/fulfillment"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/oauth"
	experimentalcredentials "google.golang.org/grpc/experimental/credentials"
	"k8s.io/klog/v2"
)

var (
	version   = "dev"
	gitCommit = "unknown"
)

func main() {
	klog.InitFlags(nil)

	csiEndpoint := flag.String("csi-endpoint", "unix:///csi/osac/csi.sock", "CSI endpoint this driver listens on")
	nodeID := flag.String("node-id", "", "Node ID for NodeGetInfo")
	fulfillmentEndpoint := flag.String("fulfillment-endpoint", "",
		"gRPC endpoint for the OSAC fulfillment service (uses stub if empty)")
	fulfillmentTokenFile := flag.String("fulfillment-token-file", "",
		"Path to a file containing the bearer token for fulfillment-service authentication")
	grpcPlaintext := flag.Bool("grpc-plaintext", false, "Use insecure (plaintext) gRPC connection")
	grpcInsecure := flag.Bool("grpc-insecure", false, "Skip TLS server certificate verification")
	vendorSocketsFlag := flag.String("vendor-sockets", "",
		"Comma-separated backend=socketpath pairs (e.g. ontap=/csi/trident/csi.sock)")
	driverName := flag.String("driver-name", "csi.osac.openshift.io", "CSI driver name")

	flag.Parse()

	if *nodeID == "" {
		fmt.Fprintf(os.Stderr, "Error: --node-id is required\n")
		os.Exit(1)
	}

	vendorSockets, err := parseVendorSockets(*vendorSocketsFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing --vendor-sockets: %v\n", err)
		os.Exit(1)
	}

	klog.Infof("Starting OSAC CSI driver %s version %s (commit %s)", *driverName, version, gitCommit)
	klog.Infof("CSI endpoint: %s", *csiEndpoint)
	klog.Infof("Node ID: %s", *nodeID)
	klog.Infof("Vendor sockets: %v", vendorSockets)

	var fulfillmentClient fulfillment.Client
	if *fulfillmentEndpoint != "" {
		opts, err := buildGRPCDialOptions(*grpcPlaintext, *grpcInsecure, *fulfillmentTokenFile)
		if err != nil {
			klog.Fatalf("Failed to build gRPC options: %v", err)
		}
		fulfillmentClient, err = fulfillment.NewGRPCClient(*fulfillmentEndpoint, vendorSockets, opts...)
		if err != nil {
			klog.Fatalf("Failed to create fulfillment gRPC client: %v", err)
		}
		klog.Infof("Fulfillment endpoint: %s (gRPC client)", *fulfillmentEndpoint)
	} else {
		klog.Infof("No fulfillment endpoint configured, using logging stub")
		fulfillmentClient = &fulfillment.LoggingStub{}
	}
	defer func() { _ = fulfillmentClient.Close() }()

	d, err := driver.NewDriver(*driverName, version, *csiEndpoint, *nodeID, fulfillmentClient, vendorSockets)
	if err != nil {
		klog.Fatalf("Failed to create driver: %v", err)
	}

	if err := d.Run(); err != nil {
		klog.Fatalf("Failed to run driver: %v", err)
	}
}

func buildGRPCDialOptions(plaintext, insecureSkipVerify bool, tokenFile string) ([]grpc.DialOption, error) {
	var opts []grpc.DialOption

	if plaintext {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		tlsCfg := &tls.Config{
			InsecureSkipVerify: insecureSkipVerify, //nolint:gosec // user-controlled flag
		}
		opts = append(opts, grpc.WithTransportCredentials(experimentalcredentials.NewTLSWithALPNDisabled(tlsCfg)))
	}

	if tokenFile != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(
			oauth.TokenSource{TokenSource: &fileTokenSource{path: tokenFile}},
		))
	}

	return opts, nil
}

// fileTokenSource reads a bearer token from a file on each call, so
// rotated tokens are picked up without a restart.
type fileTokenSource struct {
	path string
}

func (f *fileTokenSource) Token() (*oauth2.Token, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("reading token file %s: %w", f.path, err)
	}
	return &oauth2.Token{
		AccessToken: strings.TrimSpace(string(data)),
		TokenType:   "Bearer",
	}, nil
}

func parseVendorSockets(s string) (map[string]string, error) {
	result := make(map[string]string)
	if s == "" {
		return result, nil
	}

	pairs := strings.Split(s, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid vendor socket pair %q: expected format backend=socketpath", pair)
		}

		backend := strings.TrimSpace(parts[0])
		socketPath := strings.TrimSpace(parts[1])

		if backend == "" || socketPath == "" {
			return nil, fmt.Errorf("invalid vendor socket pair %q: backend and socketpath must not be empty", pair)
		}

		result[backend] = socketPath
	}

	return result, nil
}
