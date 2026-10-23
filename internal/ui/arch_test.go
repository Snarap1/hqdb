package ui

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// TestDoesNotImportServer enforces the architecture boundary from AGENTS.md:
// nothing imports internal/server except main. The UI talks
// to the core packages directly and must never reach into the MCP tool
// layer, which would let MCP-only concerns (like server-side screening)
// leak into the terminal UI.
func TestDoesNotImportServer(t *testing.T) {
	out, err := exec.Command("go", "list", "-json", "github.com/Snarap1/hqdb/internal/ui").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	var pkg struct {
		Deps []string
	}
	if err := json.Unmarshal(out, &pkg); err != nil {
		t.Fatalf("parsing go list output: %v", err)
	}
	for _, dep := range pkg.Deps {
		if dep == "github.com/Snarap1/hqdb/internal/server" {
			t.Fatalf("internal/ui imports internal/server (directly or transitively), which is not allowed")
		}
	}
}
