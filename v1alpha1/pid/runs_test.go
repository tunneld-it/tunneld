package pid

import (
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnel-pizza/tunneld/v1alpha1/origins"
)

// TestRuns pins -l's registry: a run holding its file's lock is listed with
// its pid, key, paths and settings; a file nobody holds is cleared; a run
// with no settings is its key alone; no directory is no runs.
func TestRuns(t *testing.T) {
	dir := t.TempDir()
	p := New(WithDir(dir))
	u, _ := url.Parse("http://localhost:3000")
	run := origins.New(origins.WithDir("/work"), origins.WithOrigins(origins.NewOrigin(u)))
	release, err := p.Register(run, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer release()
	key := run.Key()
	if err := os.WriteFile(filepath.Join(dir, key+".env"), []byte("LIBTUNNEL_SPEC='{}'\nLIBTUNNEL_HOSTNAME='t.example'\nPWD='/work'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "gone.pid")
	if err := os.WriteFile(stale, []byte("99999\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runs, err := p.Runs()
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("Runs = %+v, want the one registered", runs)
	}
	r := runs[0]
	if r.Pid != os.Getpid() || r.Key != key || r.EnvPath == "" || r.Env["LIBTUNNEL_HOSTNAME"] != "t.example" || r.Env["PWD"] != "/work" {
		t.Errorf("run = %+v, want this process, its key, its settings", r)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a file nobody holds is still there: %v", err)
	}

	none, err := New(WithDir(filepath.Join(dir, "nowhere"))).Runs()
	if err != nil || len(none) != 0 {
		t.Errorf("Runs in a directory that is not there = %v, %v; want none", none, err)
	}
}

// TestEnvFile pins how a run's settings are read: NAME='value' lines, and
// nothing else.
func TestEnvFile(t *testing.T) {
	got := envFile("A='1'\nB_2='two words'\nlower='x'\n3X='y'\nC=unquoted\n\nD=''\n")
	want := map[string]string{"A": "1", "B_2": "two words", "D": ""}
	if len(got) != len(want) {
		t.Fatalf("envFile = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("envFile[%s] = %q, want %q", k, got[k], v)
		}
	}
}
