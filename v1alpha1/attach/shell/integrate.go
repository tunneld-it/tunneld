package shell

import (
	"os"
	"path/filepath"
	"strings"
)

// integrated is what a shell run with no arguments is started with so that it
// marks its commands (OSC 133): a prompt (A), a command starting (C) and one
// ending with its status (D;n), which a terminal draws as nothing. Each reads
// the person's own startup files first; the marks come after.
//
// By the shell's name: bash takes an rc file, zsh a ZDOTDIR whose files read
// the person's, fish an init command, and the POSIX shells an ENV file whose
// prompt carries $?. They have no hook before a command, so they mark no C;
// nor does bash before 4.4, which has no PS0. Any other program, or a shell
// given arguments, is started as it is.
func integrated(dir, path string, args, env []string) ([]string, []string) {
	if len(args) > 0 || dir == "" {
		return args, env
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if os.WriteFile(p, []byte(body), 0o600) != nil {
			return ""
		}
		return p
	}
	switch filepath.Base(path) {
	case "bash":
		if rc := write("bashrc", bashrc); rc != "" {
			return []string{"--rcfile", rc}, env
		}
	case "zsh":
		zdotdir := filepath.Join(dir, "zsh")
		if os.MkdirAll(zdotdir, 0o700) != nil {
			return args, env
		}
		for name, body := range map[string]string{".zshenv": zshenv, ".zprofile": zprofile, ".zshrc": zshrc} {
			if write(filepath.Join("zsh", name), body) == "" {
				return args, env
			}
		}
		user, _ := lookup(env, "ZDOTDIR")
		return args, set(env, "ZDOTDIR", zdotdir, "TUNNELD_USER_ZDOTDIR", user)
	case "fish":
		return []string{"--init-command", fishInit}, env
	case "sh", "dash", "ksh", "ksh93", "mksh", "ash":
		if rc := write("shrc", shrc); rc != "" {
			user, _ := lookup(env, "ENV")
			return args, set(env, "ENV", rc, "TUNNELD_USER_ENV", user)
		}
	}
	return args, env
}

// The marks each shell prints, as printf writes them.
const (
	markEnd    = `\033]133;D;%s\007\033]133;A\007`
	markStart  = `\033]133;C\007`
	fishEnd    = `\e]133;D;%s\a\e]133;A\a`
	fishStart  = `\e]133;C\a`
	shellStart = `$'\033]133;C\007'`
)

// bashrc is bash's: ~/.bashrc, then the marks before whatever else the
// prompt runs, so $? is still the command's.
const bashrc = `[ -f ~/.bashrc ] && . ~/.bashrc
__tunneld_prompt() { local s=$?; printf '` + markEnd + `' "$s"; return $s; }
PROMPT_COMMAND="__tunneld_prompt${PROMPT_COMMAND:+; $PROMPT_COMMAND}"
PS0=` + shellStart + `"${PS0-}"
`

// zshenv and zprofile read the person's own from their ZDOTDIR, or $HOME,
// and give zsh back this directory for the next; zshrc reads theirs, adds
// the marks, and leaves ZDOTDIR theirs for .zlogin and everything after.
const (
	zshenv = `__tunneld_zdotdir=$ZDOTDIR; ZDOTDIR=${TUNNELD_USER_ZDOTDIR:-$HOME}
[ -f "$ZDOTDIR/.zshenv" ] && . "$ZDOTDIR/.zshenv"
TUNNELD_USER_ZDOTDIR=$ZDOTDIR; ZDOTDIR=$__tunneld_zdotdir; unset __tunneld_zdotdir
`
	zprofile = `__tunneld_zdotdir=$ZDOTDIR; ZDOTDIR=$TUNNELD_USER_ZDOTDIR
[ -f "$ZDOTDIR/.zprofile" ] && . "$ZDOTDIR/.zprofile"
TUNNELD_USER_ZDOTDIR=$ZDOTDIR; ZDOTDIR=$__tunneld_zdotdir; unset __tunneld_zdotdir
`
	zshrc = `ZDOTDIR=$TUNNELD_USER_ZDOTDIR; unset TUNNELD_USER_ZDOTDIR
[ -f "$ZDOTDIR/.zshrc" ] && . "$ZDOTDIR/.zshrc"
__tunneld_precmd() { local s=$?; printf '` + markEnd + `' $s }
__tunneld_preexec() { printf '` + markStart + `' }
precmd_functions=(__tunneld_precmd $precmd_functions)
preexec_functions+=(__tunneld_preexec)
`
)

// fishInit runs after fish's own configuration.
const fishInit = `function __tunneld_prompt --on-event fish_prompt; printf '` + fishEnd + `' $status; end
function __tunneld_preexec --on-event fish_preexec; printf '` + fishStart + `'; end`

// shrc is a POSIX shell's ENV: the person's own ENV, then the marks around
// $?, which the shell expands each time it draws the prompt.
const shrc = `[ -n "$TUNNELD_USER_ENV" ] && [ -f "$TUNNELD_USER_ENV" ] && . "$TUNNELD_USER_ENV"
PS1="$(printf '\033]133;D;')\$?$(printf '\007\033]133;A\007')${PS1-\$ }"
`

// lookup is name's value in env.
func lookup(env []string, name string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// set is env with each name, value pair in kv in place of any it had.
func set(env []string, kv ...string) []string {
	out := make([]string, 0, len(env)+len(kv)/2)
	names := map[string]bool{}
	for i := 0; i+1 < len(kv); i += 2 {
		names[kv[i]] = true
	}
	for _, e := range env {
		if k, _, _ := strings.Cut(e, "="); !names[k] {
			out = append(out, e)
		}
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, kv[i]+"="+kv[i+1])
	}
	return out
}
