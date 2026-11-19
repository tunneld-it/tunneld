package shell

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestIntegrated pins what each shell given no arguments is started with:
// bash an rc file, zsh a ZDOTDIR of files that read the person's own, fish an
// init command, the POSIX shells an ENV file, each the person's own value
// kept to read; a shell given arguments, and any other program, as it is.
func TestIntegrated(t *testing.T) {
	env := []string{"HOME=/home/me", "ZDOTDIR=/home/me/.config/zsh", "ENV=/home/me/.shrc", "TERM=xterm-256color"}
	for name, check := range map[string]func(t *testing.T, dir string, args, env []string){
		"/bin/bash": func(t *testing.T, dir string, args, _ []string) {
			if len(args) != 2 || args[0] != "--rcfile" || !strings.Contains(read(t, args[1]), "[ -f ~/.bashrc ] && . ~/.bashrc") {
				t.Errorf("bash args = %q, want --rcfile to one reading ~/.bashrc", args)
			}
		},
		"/usr/bin/zsh": func(t *testing.T, dir string, args, env []string) {
			if len(args) != 0 || value(env, "ZDOTDIR") != filepath.Join(dir, "zsh") || value(env, "TUNNELD_USER_ZDOTDIR") != "/home/me/.config/zsh" {
				t.Errorf("zsh args %q, env %q; want ZDOTDIR this one, the person's kept", args, env)
			}
			for _, f := range []string{".zshenv", ".zprofile", ".zshrc"} {
				if read(t, filepath.Join(dir, "zsh", f)) == "" {
					t.Errorf("no %s", f)
				}
			}
		},
		"/usr/bin/fish": func(t *testing.T, _ string, args, _ []string) {
			if len(args) != 2 || args[0] != "--init-command" || !strings.Contains(args[1], "fish_prompt") {
				t.Errorf("fish args = %q, want an init command on fish_prompt", args)
			}
		},
		"/bin/dash": func(t *testing.T, dir string, args, env []string) {
			if len(args) != 0 || value(env, "ENV") != filepath.Join(dir, "shrc") || value(env, "TUNNELD_USER_ENV") != "/home/me/.shrc" {
				t.Errorf("dash args %q, env %q; want ENV this one, the person's kept", args, env)
			}
		},
		"/usr/bin/htop": func(t *testing.T, _ string, args, got []string) {
			if len(args) != 0 || !slices.Equal(got, env) {
				t.Errorf("htop = %q, %q; want it as it is", args, got)
			}
		},
	} {
		t.Run(filepath.Base(name), func(t *testing.T) {
			dir := t.TempDir()
			args, got := integrated(dir, name, nil, env)
			check(t, dir, args, got)
		})
	}

	if args, got := integrated(t.TempDir(), "/bin/bash", []string{"-l"}, env); !slices.Equal(args, []string{"-l"}) || !slices.Equal(got, env) {
		t.Errorf("bash -l = %q, %q; want it as it is", args, got)
	}
	if args, _ := integrated("", "/bin/bash", nil, env); len(args) != 0 {
		t.Errorf("bash with nowhere to write = %q, want it as it is", args)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func value(env []string, name string) string {
	v, _ := lookup(env, name)
	return v
}
