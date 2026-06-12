// Bench comparison gateway: grpc-gateway v2 runtime serving the same
// GET /api/health -> grpc.health.v1.Health/Check route as the svcforge bench
// gateway. The handler mirrors protoc-gen-grpc-gateway generated code
// (AnnotateContext + client call with metadata + ForwardResponseMessage) so
// the measured cost is representative of real grpc-gateway deployments.
//
// Usage: go run ./examples/bench/grpcgatewaygw [-addr 127.0.0.1:8081] [-target 127.0.0.1:9100]
package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
)

var healthCheckPattern = runtime.MustPattern(runtime.NewPattern(
	1,
	[]int{2, 0, 2, 1},
	[]string{"api", "health"},
	"",
))

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "listen address")
	target := flag.String("target", "127.0.0.1:9100", "grpc backend target")
	flag.Parse()

	conn, err := grpc.NewClient(*target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial backend: %v", err)
	}
	client := healthpb.NewHealthClient(conn)

	mux := runtime.NewServeMux()
	mux.Handle(http.MethodGet, healthCheckPattern, func(w http.ResponseWriter, req *http.Request, pathParams map[string]string) {
		ctx, cancel := context.WithCancel(req.Context())
		defer cancel()
		inboundMarshaler, outboundMarshaler := runtime.MarshalerForRequest(mux, req)
		annotatedContext, err := runtime.AnnotateContext(ctx, mux, req, "/grpc.health.v1.Health/Check", runtime.WithHTTPPathPattern("/api/health"))
		if err != nil {
			runtime.HTTPError(ctx, mux, outboundMarshaler, w, req, err)
			return
		}
		resp, md, err := requestHealthCheck(annotatedContext, inboundMarshaler, client)
		annotatedContext = runtime.NewServerMetadataContext(annotatedContext, md)
		if err != nil {
			runtime.HTTPError(annotatedContext, mux, outboundMarshaler, w, req, err)
			return
		}
		runtime.ForwardResponseMessage(annotatedContext, mux, outboundMarshaler, w, req, resp, mux.GetForwardResponseOptions()...)
	})

	log.Printf("grpc-gateway bench listening on %s -> %s", *addr, *target)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func requestHealthCheck(ctx context.Context, _ runtime.Marshaler, client healthpb.HealthClient) (proto.Message, runtime.ServerMetadata, error) {
	var protoReq healthpb.HealthCheckRequest
	var metadata runtime.ServerMetadata
	msg, err := client.Check(ctx, &protoReq, grpc.Header(&metadata.HeaderMD), grpc.Trailer(&metadata.TrailerMD))
	return msg, metadata, err
}
