package ui

import (
	"testing"

	"github.com/Snarap1/hqdb/internal/config"
)

func TestProfileNodesSortedByName(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]map[string]any{
		"zeta":  {"adapter": "redis"},
		"alpha": {"adapter": "postgres"},
	}}
	nodes := profileNodes(cfg)
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}
	if nodes[0].name != "alpha" || nodes[1].name != "zeta" {
		t.Errorf("got order %q, %q; want alpha, zeta", nodes[0].name, nodes[1].name)
	}
}

func TestProfileNodesShowsAdapter(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]map[string]any{
		"pg-dev": {"adapter": "postgres"},
	}}
	nodes := profileNodes(cfg)
	if nodes[0].adapter != "postgres" {
		t.Errorf("adapter = %q, want postgres", nodes[0].adapter)
	}
	if nodes[0].broken() {
		t.Errorf("broken() = true for a valid profile")
	}
}

func TestProfileNodesMarksUnknownOptionAsBroken(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]map[string]any{
		"bad-dev": {"adapter": "postgres", "not_a_real_key": "x"},
	}}
	nodes := profileNodes(cfg)
	if !nodes[0].broken() {
		t.Fatalf("broken() = false, want true for an invalid profile table")
	}
	if nodes[0].validateErr == nil || nodes[0].validateErr.Error() == "" {
		t.Errorf("validateErr is empty, want the full validation error")
	}
	if nodes[0].adapter != "postgres" {
		t.Errorf("adapter = %q, want postgres even when broken", nodes[0].adapter)
	}
}

func TestProfileNodesMarksUnknownAdapterAsBroken(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]map[string]any{
		"weird-dev": {"adapter": "oracle"},
	}}
	nodes := profileNodes(cfg)
	if !nodes[0].broken() {
		t.Fatalf("broken() = false, want true for an unsupported adapter")
	}
	if nodes[0].adapter != "oracle" {
		t.Errorf("adapter = %q, want oracle (the adapter it declared)", nodes[0].adapter)
	}
}
