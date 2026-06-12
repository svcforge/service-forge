// Bench backend: a minimal gRPC server exposing grpc.health.v1.Health,
// shared by every gateway under test so comparisons hit identical upstreams.
//
// Usage: go run ./examples/bench/backend [-addr 127.0.0.1:9100]
package main

import (
	"flag"
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9100", "listen address")
	flag.Parse()

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}
	server := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	log.Printf("bench backend listening on %s", *addr)
	if err := server.Serve(listener); err != nil {
		log.Fatal(err)
	}
}
