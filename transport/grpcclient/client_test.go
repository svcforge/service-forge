package grpcclient

import (
	"context"
	"sync"
	"testing"

	"github.com/svcforge/service-forge/ports/registry"
	"google.golang.org/grpc"
)

type fixedResolver []registry.ServiceInstance

func (r fixedResolver) Resolve(context.Context, string) ([]registry.ServiceInstance, error) {
	return r, nil
}

func TestDialServiceRotatesUniqueTargets(t *testing.T) {
	d := NewDialer(fixedResolver{
		{Address: "::1", Port: 9000},
		{Address: "127.0.0.1", Port: 9000},
		{Address: "::1", Port: 9000},
	})
	defer d.Close()
	for _, want := range []string{"127.0.0.1:9000", "[::1]:9000", "127.0.0.1:9000"} {
		conn, err := d.DialService(context.Background(), "example")
		if err != nil {
			t.Fatal(err)
		}
		if conn.Target() != want {
			t.Fatalf("target = %q, want %q", conn.Target(), want)
		}
	}
}

func TestConcurrentDialTargetReusesConnection(t *testing.T) {
	d := NewDialer(nil)
	defer d.Close()
	results := make(chan *grpc.ClientConn, 24)
	var wg sync.WaitGroup
	for range cap(results) {
		wg.Go(func() {
			conn, err := d.DialTarget(context.Background(), "127.0.0.1:9000")
			if err != nil {
				t.Error(err)
			}
			results <- conn
		})
	}
	wg.Wait()
	close(results)
	first := <-results
	for conn := range results {
		if conn != first {
			t.Fatal("concurrent calls returned different connections")
		}
	}
}
