package pid

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestSummary pins what a run left in the background is handed back with,
// as the npm launcher printed it: its directory, pid and log, then what it
// shares, wrapped, and where each origin answers; the multiview line with a
// panel; less with no cached settings; already for a run that was going.
func TestSummary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("paths spelled with / and quoting for a POSIX shell")
	}
	t.Setenv("HOME", "/home/me")
	for name, tc := range map[string]struct {
		run     Run
		already bool
		want    []string
	}{
		"one program": {
			Run{Pid: 42, LogPath: "/home/me/.cache/tunneld/_3000_bash.log", Env: map[string]string{
				"PWD": "/home/me/project", "LIBTUNNEL_HOSTNAME": "t.example", "TUNNELD_ORIGINS": "exec:///bin/bash?arg=-l",
			}}, false,
			[]string{
				"",
				"🚇 tunneld is now running in the background",
				"",
				"    cwd  ~/project",
				"    pid  42",
				"    log  ~/.cache/tunneld/_3000_bash.log",
				"",
				"  Your application (/bin/bash -l) is now available, tunneled through",
				"  https://t.example/, at the following address:",
				"",
				"  https://t.example/  → /bin/bash -l",
				"",
				"",
			},
		},
		"several, with the panel": {
			Run{Pid: 42, Env: map[string]string{
				"LIBTUNNEL_HOSTNAME": "t.example", "TUNNELD_MULTIVIEW": "true",
				"TUNNELD_ORIGINS": "http://localhost:3000,exec:///usr/bin/claude?arg=--resume",
			}}, false,
			[]string{
				"",
				"🚇 tunneld is now running in the background",
				"",
				"    pid  42",
				"",
				"  Your applications (http://localhost:3000, /usr/bin/claude --resume) are now",
				"  available, tunneled through https://t.example/, at the following addresses:",
				"",
				"  https://t.example/    → multiview",
				"  https://t.example/?0  → http://localhost:3000",
				"  https://t.example/?1  → /usr/bin/claude --resume",
				"",
				"",
			},
		},
		"no cached settings": {
			Run{Pid: 42, LogPath: "/var/log/__no_cache__3000.log"}, false,
			[]string{
				"",
				"🚇 tunneld is now running in the background",
				"",
				"    pid  42",
				"    log  /var/log/__no_cache__3000.log",
				"",
				"  Your application is now available at the address above.",
				"",
				"",
			},
		},
		"already going": {
			Run{Pid: 42, Env: map[string]string{"LIBTUNNEL_HOSTNAME": "t.example", "TUNNELD_ORIGINS": "http://localhost:3000"}}, true,
			nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var b strings.Builder
			Summary(&b, tc.run, tc.already, Style{Columns: 78})
			if tc.want == nil {
				if !strings.Contains(b.String(), "🚇 tunneld is already running in the background\n") {
					t.Errorf("summary =\n%s\nwant it to say already", b.String())
				}
				return
			}
			if got, want := b.String(), strings.Join(tc.want, "\n"); got != want {
				t.Errorf("summary =\n%q\nwant\n%q", got, want)
			}
		})
	}
}

// TestSummaryNarrow pins the prose wrapped to a narrow terminal, a word never
// split, and color only where asked.
func TestSummaryNarrow(t *testing.T) {
	run := Run{Pid: 42, Env: map[string]string{"LIBTUNNEL_HOSTNAME": "t.example", "TUNNELD_ORIGINS": "http://localhost:3000"}}
	var b strings.Builder
	Summary(&b, run, false, Style{Columns: 30})
	prose := 0
	for _, line := range strings.Split(b.String(), "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "    ") || strings.Contains(line, "→") {
			continue
		}
		prose++
		if len(line) > 28 && !strings.Contains(line, "https://") {
			t.Errorf("prose line %q is wider than 28 columns", line)
		}
	}
	if prose < 3 {
		t.Errorf("summary =\n%s\nwant the prose wrapped onto several lines", b.String())
	}
	if strings.Contains(b.String(), "\x1b[") {
		t.Error("painted without being asked to")
	}
	b.Reset()
	Summary(&b, run, false, Style{Color: true, Columns: 78})
	if !strings.Contains(b.String(), "\x1b[36mhttps://t.example/\x1b[0m") {
		t.Errorf("summary with color =\n%q\nwant the address in cyan", b.String())
	}
}

// TestAddresses pins what a run is printed as on stdout: the bare address for
// one origin or a panel, ?n for each of several, nothing without settings.
func TestAddresses(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want []string
	}{
		"one":       {map[string]string{"LIBTUNNEL_HOSTNAME": "t.example", "TUNNELD_ORIGINS": "http://localhost:3000"}, []string{"https://t.example/"}},
		"several":   {map[string]string{"LIBTUNNEL_HOSTNAME": "t.example", "TUNNELD_ORIGINS": "a,b"}, []string{"https://t.example/?0", "https://t.example/?1"}},
		"the panel": {map[string]string{"LIBTUNNEL_HOSTNAME": "t.example", "TUNNELD_ORIGINS": "a,b", "TUNNELD_MULTIVIEW": "true"}, []string{"https://t.example/"}},
		"nothing":   {nil, nil},
	} {
		if got := Addresses(Run{Env: tc.env}); !slices.Equal(got, tc.want) {
			t.Errorf("%s: Addresses = %q, want %q", name, got, tc.want)
		}
	}
}

// TestList pins -l's lines: the pid padded to the widest, then the address,
// directory and origins, or the key with no settings.
func TestList(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("paths spelled with / and quoting for a POSIX shell")
	}
	t.Setenv("HOME", "/home/me")
	runs := []Run{
		{Pid: 7, Key: "_3000_bash", Env: map[string]string{"LIBTUNNEL_HOSTNAME": "t.example", "PWD": "/home/me/project", "TUNNELD_ORIGINS": "exec:///bin/bash?arg=-l"}},
		{Pid: 12345, Key: "__no_cache__4000"},
	}
	var b strings.Builder
	List(&b, runs)
	if want := "7      https://t.example/  ~/project  /bin/bash -l\n12345  __no_cache__4000\n"; b.String() != want {
		t.Errorf("List =\n%q\nwant\n%q", b.String(), want)
	}
}

// TestListJSON pins -l -s: one JSON array, [] for none, each run its pid and
// key and what its settings and log give, never its spec.
func TestListJSON(t *testing.T) {
	dir := t.TempDir()
	env, log := filepath.Join(dir, "k.env"), filepath.Join(dir, "k.log")
	runs := []Run{
		{Pid: 7, Key: "k", EnvPath: env, LogPath: log, Env: map[string]string{
			"LIBTUNNEL_SPEC": `{"secret":"s3cret"}`, "LIBTUNNEL_HOSTNAME": "t.example", "PWD": "/work",
			"TUNNELD_ORIGINS": "http://localhost:3000,exec:///bin/bash?arg=-l",
		}},
		{Pid: 12345, Key: "__no_cache__4000"},
	}
	var b strings.Builder
	if err := ListJSON(&b, runs); err != nil {
		t.Fatal(err)
	}
	quoted := func(s string) string { q, _ := json.Marshal(s); return string(q) }
	want := `[{"pid":7,"key":"k","address":"https://t.example/","addresses":["https://t.example/?0","https://t.example/?1"],"cwd":"/work","origins":["http://localhost:3000","exec:///bin/bash?arg=-l"],"env":` + quoted(env) + `,"log":` + quoted(log) + `},{"pid":12345,"key":"__no_cache__4000"}]` + "\n"
	if b.String() != want {
		t.Errorf("ListJSON =\n%s\nwant\n%s", b.String(), want)
	}
	if strings.Contains(b.String(), "s3cret") {
		t.Errorf("ListJSON carries the spec: %s", b.String())
	}
	b.Reset()
	if err := ListJSON(&b, nil); err != nil || b.String() != "[]\n" {
		t.Errorf("ListJSON of none = %q, %v; want []", b.String(), err)
	}
}

// TestLabel pins an origin as a person reads it: a program and its
// arguments, each quoted so it pastes back as itself; anything else as it is.
func TestLabel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cmd.exe quoting")
	}
	for origin, want := range map[string]string{
		"exec:///bin/bash?arg=-l":                "/bin/bash -l",
		"exec:///usr/bin/claude?arg=fix+the+bug": "/usr/bin/claude 'fix the bug'",
		"exec:///bin/sh?arg=it%27s":              `/bin/sh 'it'\''s'`,
		"http://localhost:3000":                  "http://localhost:3000",
		"attach://dockerd/web":                   "attach://dockerd/web",
	} {
		if got := label(origin); got != want {
			t.Errorf("label(%q) = %q, want %q", origin, got, want)
		}
	}
}

// TestListJSONPathsAreAbsolute pins the paths of a run's files as absolute,
// whatever directory the registry was named by.
func TestListJSONPathsAreAbsolute(t *testing.T) {
	t.Chdir(t.TempDir())
	var b strings.Builder
	if err := ListJSON(&b, []Run{{Pid: 7, Key: "k", EnvPath: "cache/k.env", LogPath: "cache/k.log", Env: map[string]string{"PWD": "/work"}}}); err != nil {
		t.Fatal(err)
	}
	var got []struct{ Env, Log string }
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil || len(got) != 1 {
		t.Fatalf("ListJSON = %s, %v", b.String(), err)
	}
	for name, p := range map[string]string{"env": got[0].Env, "log": got[0].Log} {
		if !filepath.IsAbs(p) {
			t.Errorf("%s = %q, want an absolute path", name, p)
		}
	}
}
