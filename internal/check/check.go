// Package check validates a config the way a person does before handing it
// to an agent or a teammate: every Profile must parse, and, when probing,
// must answer one cheap read (hqdb check).
package check

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/catalog"
	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/query"
	"github.com/Snarap1/hqdb/internal/rediscmd"
)

// probeTimeout bounds one Profile's connection and probe.
const probeTimeout = 15 * time.Second

// Options selects what Run checks.
type Options struct {
	// Profiles limits the check to these names; empty means every Profile.
	Profiles []string
	// Probe connects to each Profile and runs one read: SELECT 1 on SQL
	// adapters, PING on redis, and a root get_schema on mongodb.
	Probe bool
}

// Run writes one line per Profile (name, adapter, access, ok or FAIL, and
// the error or "(default)") to w and reports whether every one passed.
func Run(ctx context.Context, cfg *config.Config, opts Options, w io.Writer) bool {
	names := opts.Profiles
	if len(names) == 0 {
		names = config.SortedProfileNames(cfg)
	}
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	pass := true
	for _, name := range names {
		adapter, access, err := checkProfile(ctx, cfg, name, opts.Probe)
		status, detail := "ok", ""
		if err != nil {
			pass = false
			status, detail = "FAIL", err.Error()
		} else if name == cfg.DefaultProfile {
			detail = "(default)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", name, adapter, access, status, detail)
	}
	tw.Flush()
	// tabwriter pads the empty last cell too; drop that trailing space.
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
	return pass
}

// checkProfile validates one Profile and, when probe is set, runs its
// probe. adapter and access are "-" when they cannot be determined.
func checkProfile(ctx context.Context, cfg *config.Config, name string, probe bool) (adapter, access string, err error) {
	p, ok := cfg.Profiles[name]
	if !ok {
		return "-", "-", fmt.Errorf("unknown profile %q", name)
	}
	adapter = config.AdapterName(p)
	if _, err := config.Validate(p); err != nil {
		return adapter, "-", err
	}
	a, _ := config.ProfileAccess(p)
	if !probe {
		return adapter, string(a), nil
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return adapter, string(a), runProbe(ctx, p)
}

// runProbe opens p and runs its adapter's cheapest read.
func runProbe(ctx context.Context, p map[string]any) error {
	conn, err := adapters.Open(p)
	if err != nil {
		return err
	}
	defer conn.Close()
	switch c := conn.(type) {
	case *adapters.SQL:
		_, err = query.Run(ctx, c.SQL(), "SELECT 1", 1)
	case *adapters.Redis:
		_, err = rediscmd.Run(ctx, c, "PING", nil)
	default:
		_, err = catalog.GetSchema(ctx, conn, nil, "", false, catalog.DefaultLimits)
	}
	return err
}
