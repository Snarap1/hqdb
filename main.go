// Command hqdb is a terminal database client for humans and AI agents.
// With no arguments it opens the terminal UI; "hqdb mcp" serves the same
// Profiles to an agent over MCP on stdio; "hqdb check" validates the
// config.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/check"
	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/consolestate"
	"github.com/Snarap1/hqdb/internal/server"
	"github.com/Snarap1/hqdb/internal/ui"
)

// version is set by the release build (-ldflags "-X main.version=...");
// a go install build reports its module version instead.
var version = ""

const usage = `hqdb - terminal database client for humans and AI agents

Usage:
  hqdb                      open the terminal UI
  hqdb mcp                  serve the Profiles to an AI agent over MCP (stdio)
  hqdb check [flags] [profile ...]
                            validate the config and probe each Profile
  hqdb version              print the version

Config: hqdb.toml, .hqdb.toml, $XDG_CONFIG_HOME/hqdb/config.toml,
[tool.hqdb] in pyproject.toml, or the file named by HQDB_CONFIG.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		return runTUI()
	}
	switch args[0] {
	case "mcp":
		return runMCP()
	case "check":
		return runCheck(args[1:])
	case "version", "--version", "-v":
		fmt.Println("hqdb " + currentVersion())
		return 0
	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "hqdb: unknown command %q\n\n%s", args[0], usage)
	return 2
}

func runTUI() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hqdb: %v\n", err)
		return 1
	}
	if _, err := tea.NewProgram(ui.New(cfg, ui.WithStateDir(consolestate.DefaultDir()))).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "hqdb: %v\n", err)
		return 1
	}
	return 0
}

func runMCP() int {
	// stdout is the MCP protocol channel: every diagnostic goes to stderr.
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	if err := server.Serve(context.Background(), currentVersion()); err != nil {
		log.Printf("hqdb: %v", err)
		return 1
	}
	return 0
}

func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	noProbe := fs.Bool("no-probe", false, "only parse and validate the config; do not connect")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "hqdb check: %v\nusage: hqdb check [--no-probe] [profile ...]\n", err)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hqdb: %v\n", err)
		return 1
	}
	if len(cfg.Profiles) == 0 && fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "hqdb: no profiles found; see hqdb help for where the config is read from")
		return 1
	}
	if !check.Run(context.Background(), cfg, check.Options{Profiles: fs.Args(), Probe: !*noProbe}, os.Stdout) {
		return 1
	}
	return 0
}

// currentVersion is the release version, else the module version a go
// install build carries, else "dev".
func currentVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
