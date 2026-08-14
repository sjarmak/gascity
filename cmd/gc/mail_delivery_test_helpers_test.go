package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeMailDeliveryBinaryCity(t *testing.T, cityPath, cityName, identity, adapterPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(cityPath, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	cityTOML := fmt.Sprintf(`[workspace]
name = %q
prefix = "gc"

[beads]
provider = "file"
conditional_writes = "require"

[session]
provider = %q

[[agent]]
name = %q
provider = %q
start_command = "true"

[[named_session]]
name = %q
template = %q
scope = "city"
mode = "always"
`, cityName, "exec:"+adapterPath, identity, "exec:"+adapterPath, identity, identity)
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(cityTOML), 0o600); err != nil {
		t.Fatal(err)
	}
}
