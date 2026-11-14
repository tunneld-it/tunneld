//go:build unix

package stdioutil

import (
	"io"
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

// onto points fd at a fresh pipe for the rest of the test, and returns a
// read of everything written there, taken once fd is put back.
func onto(t *testing.T, fd int) func() string {
	t.Helper()
	saved, err := unix.Dup(fd)
	if err != nil {
		t.Fatalf("dup(%d): %v", fd, err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if err := unix.Dup2(int(w.Fd()), fd); err != nil {
		t.Fatalf("dup2 onto %d: %v", fd, err)
	}
	_ = w.Close()
	back := func() { _ = unix.Dup2(saved, fd) }
	t.Cleanup(func() { back(); _ = unix.Close(saved); _ = r.Close() })
	return func() string {
		back()
		b, _ := io.ReadAll(r)
		return string(b)
	}
}

// TestLockStdout pins the lock: while it holds, only the client's copy
// reaches stdout, and os.Stdout and a child inheriting it reach stderr; once
// restored, os.Stdout reaches stdout again.
func TestLockStdout(t *testing.T) {
	stdout := onto(t, unix.Stdout)
	stderr := onto(t, unix.Stderr)

	client, restore, err := LockStdout()
	if err != nil {
		t.Fatalf("LockStdout: %v", err)
	}
	_, _ = io.WriteString(os.Stdout, "stray ")
	child := exec.Command("/bin/sh", "-c", "printf child")
	child.Stdout = os.Stdout
	if err := child.Run(); err != nil {
		t.Fatalf("child: %v", err)
	}
	_, _ = io.WriteString(client, "rpc ")
	restore()
	_, _ = io.WriteString(os.Stdout, "after")
	_ = client.Close()

	if got := stdout(); got != "rpc after" {
		t.Errorf("stdout = %q, want the client's write and, once restored, os.Stdout's", got)
	}
	if got := stderr(); got != "stray child" {
		t.Errorf("stderr = %q, want what os.Stdout and the child wrote while locked", got)
	}
}
