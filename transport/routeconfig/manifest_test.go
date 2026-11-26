package routeconfig

import (
	"testing"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func TestAutomaticPublicationSkipsUnannotatedServices(t *testing.T) {
	service := healthpb.File_grpc_health_v1_health_proto.Services().Get(0)
	data, err := EncodeAnnotated([]string{string(service.FullName())}, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("unannotated service generated a route manifest: %d bytes", len(data))
	}
}
