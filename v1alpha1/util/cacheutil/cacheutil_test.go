package cacheutil

import (
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/tunnel-pizza/tunneld/v1"
)

// TestDir pins where a run's files go: $TUNNELD_CACHE_DIR whole when set,
// tunneld under the user's cache directory when not.
func TestDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", base)
	t.Setenv("HOME", base)
	t.Setenv("LocalAppData", base)

	t.Setenv(v1.CacheDirEnv, "")
	user, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache directory: %v", err)
	}
	if got, want := Dir(), filepath.Join(user, "tunneld"); got != want {
		t.Errorf("Dir() unset = %q, want %q", got, want)
	}

	set := filepath.Join(t.TempDir(), "volume")
	t.Setenv(v1.CacheDirEnv, set)
	if got := Dir(); got != set {
		t.Errorf("Dir() with %s=%s = %q, want it as given", v1.CacheDirEnv, set, got)
	}
}
