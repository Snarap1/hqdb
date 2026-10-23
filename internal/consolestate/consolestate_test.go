package consolestate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDirUsesXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/x/state")
	t.Setenv("HOME", "/h")
	if got := DefaultDir(); got != "/x/state/hqdb" {
		t.Fatalf("DefaultDir = %q", got)
	}
}

func TestDefaultDirFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/h")
	if got := DefaultDir(); got != filepath.Join("/h", ".local", "state", "hqdb") {
		t.Fatalf("DefaultDir = %q", got)
	}
}

func TestBufferRoundTripsPerProfile(t *testing.T) {
	s := New(t.TempDir())
	if err := s.SaveBuffer("pg-dev", "SELECT 1;\n"); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadBuffer("pg-dev"); got != "SELECT 1;\n" {
		t.Fatalf("LoadBuffer = %q", got)
	}
	if got := s.LoadBuffer("other"); got != "" {
		t.Fatalf("other profile buffer = %q", got)
	}
}

func TestProfileNamesCannotEscapeTheDirectory(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "hq"))
	if err := s.SaveBuffer("../evil/x", "a"); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadBuffer("../evil/x"); got != "a" {
		t.Fatalf("LoadBuffer = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); !os.IsNotExist(err) {
		t.Fatalf("profile name escaped the state directory")
	}
}

func TestHistoryAppendsInOrderAndSkipsImmediateRepeats(t *testing.T) {
	s := New(t.TempDir())
	for _, stmt := range []string{"SELECT 1", "SELECT\n2", "SELECT\n2", "SELECT 3"} {
		if err := s.AppendHistory("pg", stmt); err != nil {
			t.Fatal(err)
		}
	}
	got := s.History("pg")
	want := []string{"SELECT 1", "SELECT\n2", "SELECT 3"}
	if len(got) != len(want) {
		t.Fatalf("History = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("History = %q", got)
		}
	}
}

func TestHistoryIsCappedToTheNewestEntries(t *testing.T) {
	s := New(t.TempDir())
	for i := 0; i < MaxHistory+5; i++ {
		_ = s.AppendHistory("pg", string(rune('a'+i%26))+string(rune('0'+i%10)))
	}
	h := s.History("pg")
	if len(h) != MaxHistory {
		t.Fatalf("len(History) = %d", len(h))
	}
}

func TestMissingOrUnusableDirectoryIsHarmless(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Store{New(filepath.Join(t.TempDir(), "missing")), New(filepath.Join(file, "hq")), nil} {
		if got := s.LoadBuffer("pg"); got != "" {
			t.Fatalf("LoadBuffer = %q", got)
		}
		if got := s.History("pg"); len(got) != 0 {
			t.Fatalf("History = %q", got)
		}
	}
	if err := New(filepath.Join(file, "hq")).SaveBuffer("pg", "x"); err == nil {
		t.Fatal("SaveBuffer under a file succeeded")
	}
	var nilStore *Store
	if err := nilStore.SaveBuffer("pg", "x"); err != nil {
		t.Fatal(err)
	}
	if err := nilStore.AppendHistory("pg", "x"); err != nil {
		t.Fatal(err)
	}
}

func TestUnreadableDirectoryReadsEmpty(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	dir := t.TempDir()
	s := New(dir)
	_ = s.SaveBuffer("pg", "x")
	_ = s.AppendHistory("pg", "y")
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if got := s.LoadBuffer("pg"); got != "" {
		t.Fatalf("LoadBuffer = %q", got)
	}
	if got := s.History("pg"); len(got) != 0 {
		t.Fatalf("History = %q", got)
	}
}
