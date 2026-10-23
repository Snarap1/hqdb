package server

import (
	"testing"

	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/readonly"
)

func TestScreen(t *testing.T) {
	cases := []struct {
		name    string
		access  config.Access
		sql     string
		wantErr string
	}{
		{"read-only allows a read", config.ReadOnly, "SELECT 1", ""},
		{"read-only refuses a write", config.ReadOnly, "DELETE FROM t",
			"Refused (read-only profile): " + readonly.Violation("DELETE FROM t")},
		{"read-write runs a write unscreened", config.ReadWrite, "DELETE FROM t", ""},
		{"read-write runs stacked statements", config.ReadWrite, "SELECT 1; DROP TABLE t", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := screen(c.access, readonly.Violation(c.sql))
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != c.wantErr {
				t.Errorf("got %q, want %q", got, c.wantErr)
			}
		})
	}
}
