//go:build unix

package pid

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	v1 "github.com/tunnel-pizza/tunneld/v1"
	"golang.org/x/sys/unix"
)

// standIn is a program playing the run Background starts: it does what body
// says with $TUNNELD_NOTIFY_PID and $TUNNELD_STATUS_FD, as the binary does.
func standIn(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestBackground pins -d's half of the launcher's protocol: told who to
// signal and where the status pipe is, in a session of its own, a run that
// signals is left running; one that ends first answers its status; one
// refused for a run already going answers that run's status.
func TestBackground(t *testing.T) {
	t.Run("up", func(t *testing.T) {
		exe := standIn(t, `[ "$TUNNELD_STATUS_FD" = 3 ] || exit 9
kill -USR2 "$TUNNELD_NOTIFY_PID"
exec sleep 30`)
		s, err := Background(exe, nil, os.Environ())
		if err != nil || s.Pid <= 0 {
			t.Fatalf("Background = %+v, %v; want the run's pid", s, err)
		}
		defer syscall.Kill(s.Pid, syscall.SIGKILL)
		if err := syscall.Kill(s.Pid, 0); err != nil {
			t.Errorf("the run is not running after Background returned: %v", err)
		}
		if sid, err := unix.Getsid(s.Pid); err != nil || sid != s.Pid {
			t.Errorf("the run's session = %d, %v; want one of its own, %d", sid, err, s.Pid)
		}
	})

	t.Run("ends first", func(t *testing.T) {
		_, err := Background(standIn(t, "exit 3"), nil, os.Environ())
		var exit v1.ExitError
		if !errors.As(err, &exit) || exit.Code != 3 {
			t.Errorf("Background = %v, want an ExitError of 3", err)
		}
	})

	t.Run("already going", func(t *testing.T) {
		exe := standIn(t, `printf '{"pid":4242,"env":"/c/k.env","log":"/c/k.log"}\n' >&3
exit `+strconv.Itoa(v1.RunningCode))
		s, err := Background(exe, nil, os.Environ())
		if !errors.Is(err, v1.ErrRunning) || s != (Status{Pid: 4242, Env: "/c/k.env", Log: "/c/k.log"}) {
			t.Errorf("Background = %+v, %v; want the status of the run going, and ErrRunning", s, err)
		}
	})

	t.Run("refused with no status", func(t *testing.T) {
		_, err := Background(standIn(t, "exit "+strconv.Itoa(v1.RunningCode)), nil, os.Environ())
		var exit v1.ExitError
		if !errors.As(err, &exit) || exit.Code != v1.RunningCode || errors.Is(err, v1.ErrRunning) {
			t.Errorf("Background = %v, want its exit status and no run to hand back", err)
		}
	})
}
