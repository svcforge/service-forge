package templates

import (
	"os"
	"path/filepath"
	"testing"

	gatewaypolicy "github.com/svcforge/service-forge/api/proto/svcforge/gateway/v1"
)

func TestEnsureGatewayPolicyPreservesConflictingFile(t *testing.T) {
	root := t.TempDir()
	if err := EnsureGatewayPolicy(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, gatewaypolicy.ProjectPath)
	const custom = "custom local policy"
	if err := os.WriteFile(path, []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGatewayPolicy(root); err == nil {
		t.Fatal("expected incompatible schema error")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != custom {
		t.Fatalf("conflicting policy was changed: %q (%v)", data, err)
	}
}
