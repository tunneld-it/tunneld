package stdioutil

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestLockStdout pins the lock: while it holds, os.Stdout and the process's
// standard output handle are stderr's and the client keeps the original;
// restore puts both back.
func TestLockStdout(t *testing.T) {
	was := os.Stdout
	client, restore, err := LockStdout()
	if err != nil {
		t.Fatalf("LockStdout: %v", err)
	}
	t.Cleanup(restore)

	if client != was {
		t.Error("client is not the stdout the lock found")
	}
	if os.Stdout != os.Stderr {
		t.Error("os.Stdout is not stderr while locked")
	}
	if h, _ := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); h != windows.Handle(os.Stderr.Fd()) {
		t.Errorf("standard output handle = %v, want stderr's while locked", h)
	}

	restore()
	if os.Stdout != was {
		t.Error("os.Stdout is not restored")
	}
	if h, _ := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); h != windows.Handle(was.Fd()) {
		t.Errorf("standard output handle = %v, want the original once restored", h)
	}
}
