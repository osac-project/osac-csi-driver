module github.com/osac-project/osac-csi-driver

go 1.26.3

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.11-20260709200747-435963d16310.1
	github.com/container-storage-interface/spec v1.12.0
	golang.org/x/oauth2 v0.36.0
	google.golang.org/genproto/googleapis/api v0.0.0-20260226221140-a57be14db171
	google.golang.org/grpc v1.81.1
	google.golang.org/protobuf v1.36.11
	k8s.io/klog/v2 v2.140.0
)

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
)
