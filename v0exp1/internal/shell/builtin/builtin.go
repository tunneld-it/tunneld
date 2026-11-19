package builtin

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	v1 "github.com/tunnel-pizza/tunneld/v1"
	"github.com/tunnel-pizza/tunneld/v1alpha1/origins"
	"src.elv.sh/pkg/prog"
	elvish "src.elv.sh/pkg/shell"
)

// Arg is what a process is started with to be the shell built into
// tunneld rather than tunneld: its one argument, and nothing else. Spelled so
// nothing a person types at tunneld collides with it.
const Arg = "--tunneld-builtin-shell"

// ErrNoTerminal is Origin's answer on a platform with no pseudo-terminals.
var ErrNoTerminal = errors.New("the built-in shell needs a pseudo-terminal, which this platform does not have")

// init turns a process started with Arg into the built-in shell before
// anything else in it runs, and never returns.
//
// An init rather than a branch in main, because the executable is whichever
// program links tunneld — tunneld's own, or one that embeds it under a verb of
// its own — and an embedding program's main knows nothing about this. Any
// binary that links this package answers Arg the same way, and nothing
// else changes for it: the check is one comparison of its arguments.
//
// The same goes for the shell's commands: this executable run under one of
// their names, from the directory the shell put on its $PATH, is that command
// (see commands.go).
func init() {
	if len(os.Args) == 2 && os.Args[1] == Arg {
		os.Exit(Run())
	}
	if name, ok := command(); ok {
		os.Exit(runCommand(name))
	}
}

// Origin is the origin of the shell built into tunneld: this executable, run
// with Arg, as an ordinary program origin. So it is served exactly the
// way any program is — on a pseudo-terminal that is its controlling terminal,
// which is what makes Ctrl-C reach what it runs — and the only thing that
// knows it is not a program on disk is init above.
//
// Not on Windows, which has no pseudo-terminal to serve it on — and whose
// C:\... path has no spelling in an exec:// URL besides — so a run there with
// no shell of its own is refused for having no origin, as it was before.
func Origin() (v1.Origin, error) {
	if runtime.GOOS == "windows" {
		return v1.Origin{}, ErrNoTerminal
	}
	exe, err := os.Executable()
	if err != nil {
		return v1.Origin{}, fmt.Errorf("find this executable to run its built-in shell: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	u := url.URL{Scheme: string(v1.ExecScheme), Path: exe, RawQuery: url.Values{v1.ArgKey: {Arg}}.Encode()}
	return origins.NewOrigin(&u), nil
}

// Run is the built-in shell: Elvish (https://elv.sh), interactive on this
// process's own terminal, until it exits; the result is its exit status.
//
// Elvish rather than a POSIX interpreter because what it is for is a person at
// a terminal: it brings its own line editor — history, completion, the arrow
// keys — where a POSIX interpreter in Go has none. Its language is its own,
// which is the trade: `export`, `$(...)` and `&&` are spelled differently.
//
// Without Elvish's storage daemon, which is a second process and a database
// under the user's state directory: history lives as long as the session,
// and nothing is left behind on a machine that only ever had a shell because
// it had none. Elvish's own rc file is read if there is one, as any shell's is.
//
// With u-root's commands — ls, cat, grep, wget and the rest (commands.go)
// — at the end of $PATH for as long as the shell runs, so a machine with none
// still has them and a machine with its own keeps using those.
//
// It marks its commands (OSC 133) as tunneld's other shells do: its rc is
// the person's own rc.elv with marks hooks after it (rc).
func Run() int {
	if remove, err := installCommands(); err != nil {
		fmt.Fprintf(os.Stderr, "tunneld: no built-in commands: %v\n", err)
	} else {
		defer remove()
	}
	args := []string{"elvish"}
	if path, remove, err := rc(); err == nil {
		defer remove()
		args = append(args, "-rc", path)
	}
	return prog.Run([3]*os.File{os.Stdin, os.Stdout, os.Stderr}, args, &elvish.Program{})
}

// marks are the hooks that make Elvish mark its commands: C once a line is
// read, D with its status once it has run, A before each prompt. A command
// that exits with a status is that status, one killed by a signal 128 and its
// number, and any other error 1.
const marks = `
set edit:after-readline = [$@edit:after-readline {|_| print "\e]133;C\a" }]
set edit:after-command = [$@edit:after-command {|m|
  var code = 0
  if (not-eq $m[error] $nil) {
    set code = 1
    try { set code = $m[error][reason][exit-status] } catch {
      try { set code = (+ 128 $m[error][reason][signal-number]) } catch { }
    }
  }
  print "\e]133;D;"$code"\a"
}]
set edit:before-readline = [$@edit:before-readline { print "\e]133;A\a" }]
`

// rc is a file holding the person's own rc.elv, where Elvish looks for it,
// then the marks, and what removes it: one file rather than an eval, so what
// their rc defines stays in the shell's own namespace.
func rc() (string, func(), error) {
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, err
		}
		config = filepath.Join(home, ".config")
	}
	own, err := os.ReadFile(filepath.Join(config, "elvish", "rc.elv"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	f, err := os.CreateTemp("", "tunneld-rc-*.elv")
	if err != nil {
		return "", nil, err
	}
	remove := func() { _ = os.Remove(f.Name()) }
	if _, err := f.Write(append(own, marks...)); err != nil {
		f.Close()
		remove()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		remove()
		return "", nil, err
	}
	return f.Name(), remove, nil
}
