package errors

import (
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
)

func TestStatusMappingsAreCustomizable(t *testing.T) {
	code := Code("CUSTOM")
	RegisterHTTPStatus(code, http.StatusTeapot)
	RegisterGRPCCode(code, codes.Aborted)

	err := New(code, "custom")
	if err.HTTPStatus != http.StatusTeapot {
		t.Fatalf("http status = %d", err.HTTPStatus)
	}
	if err.GRPCCode != codes.Aborted {
		t.Fatalf("grpc code = %s", err.GRPCCode)
	}
}
