package templates

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	gatewaypolicy "github.com/svcforge/service-forge/api/proto/svcforge/gateway/v1"
)

// EnsureGatewayPolicy installs the schema used by generated gateway policies.
// Existing projects can run it repeatedly without rewriting the file.
func EnsureGatewayPolicy(root string) error {
	path := filepath.Join(root, gatewaypolicy.ProjectPath)
	existing, err := os.ReadFile(path)
	if err == nil {
		return checkGatewayPolicy(existing)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read gateway policy: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create gateway policy directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		existing, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read gateway policy: %w", err)
		}
		return checkGatewayPolicy(existing)
	}
	if err != nil {
		return fmt.Errorf("create gateway policy: %w", err)
	}
	_, writeErr := io.WriteString(file, gatewaypolicy.Schema())
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("write gateway policy: %w", errors.Join(err, os.Remove(path)))
	}
	return nil
}

func checkGatewayPolicy(data []byte) error {
	if string(data) != gatewaypolicy.Schema() {
		return fmt.Errorf("%s differs from the framework schema", gatewaypolicy.ProjectPath)
	}
	return nil
}
