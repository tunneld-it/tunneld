package pid

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Run is a registered run: its pid, its key, and the paths of its <key>.env
// and <key>.log, those that exist, with what the .env holds.
type Run struct {
	Pid     int
	Key     string
	EnvPath string
	LogPath string
	Env     map[string]string
}

// Runs implements Pid: every run holding a <key>.pid's lock in the
// directory, sorted by key. A file whose lock nobody holds is a run that
// ended without removing it, kill -9 or a crash, and is cleared without a
// word.
func (p *PidImpl) Runs() ([]Run, error) {
	if p.dir == "" {
		return nil, nil
	}
	names, err := os.ReadDir(p.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var found []Run
	for _, entry := range names {
		key, ok := strings.CutSuffix(entry.Name(), ".pid")
		if !ok {
			continue
		}
		path := filepath.Join(p.dir, entry.Name())
		pid, held := holder(path)
		if !held {
			continue
		}
		found = append(found, p.run(key, pid))
	}
	slices.SortFunc(found, func(a, b Run) int { return strings.Compare(a.Key, b.Key) })
	return found, nil
}

// holder is the pid a run holding path's lock wrote there, and whether one
// holds it. A file nobody holds is removed, under the lock taken to find out
// where the system allows it; Windows will not delete a file held open, so
// there it goes after the close, and only if it still says what it said.
func holder(path string) (int, bool) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	if err := lock(f); err == nil {
		body, _ := os.ReadFile(path)
		if os.Remove(path) != nil {
			f.Close()
			if now, err := os.ReadFile(path); err == nil && string(now) == string(body) {
				_ = os.Remove(path)
			}
		}
		return 0, false
	} else if !errors.Is(err, errLocked) {
		return 0, false
	}
	body, _ := os.ReadFile(path)
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	return pid, err == nil && pid > 0
}

// run is the run key names, held by pid: the paths of its .env and .log,
// those that exist, and what its .env holds.
func (p *PidImpl) run(key string, pid int) Run {
	r := Run{Pid: pid, Key: key}
	if path := filepath.Join(p.dir, key+".env"); exists(path) {
		r.EnvPath = path
		if body, err := os.ReadFile(path); err == nil {
			r.Env = envFile(string(body))
		}
	}
	if path := filepath.Join(p.dir, key+".log"); exists(path) {
		r.LogPath = path
	}
	return r
}

// envFile reads the NAME='value' lines a run's settings are cached in.
func envFile(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		name, value, ok := strings.Cut(line, "=")
		if !ok || !envName(name) || len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
			continue
		}
		out[name] = value[1 : len(value)-1]
	}
	return out
}

// envName is whether name is one the cache writes: capitals, digits and
// underscores, not starting with a digit.
func envName(name string) bool {
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		return false
	}
	for _, c := range name {
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}
