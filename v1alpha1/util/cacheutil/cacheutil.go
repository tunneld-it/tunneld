// Package cacheutil is where a run's files go: its cached spec, its .pid and
// its log, one directory for all three.
package cacheutil

import (
	"os"
	"path/filepath"

	v1 "github.com/tunnel-pizza/tunneld/v1"
)

// name is the directory under the user's cache directory.
const name = "tunneld"

// Dir is the directory a run's files go in, absolute: $TUNNELD_CACHE_DIR
// when set, resolved against the working directory, else tunneld under the
// user's cache directory, else "" when there is none.
func Dir() string {
	if dir := os.Getenv(v1.CacheDirEnv); dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			return abs
		}
		return dir
	}
	if base, err := os.UserCacheDir(); err == nil {
		return filepath.Join(base, name)
	}
	return ""
}
