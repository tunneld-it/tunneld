//go:build unix

// Package stdioutil holds this process's standard streams for a protocol
// spoken over them, such as an MCP client's JSON-RPC on stdout.
package stdioutil

import (
	"os"

	"golang.org/x/sys/unix"
)

// LockStdout gives an MCP run's client the process's stdout alone. fd 1 is
// copied for the transport, close-on-exec, and then pointed at stderr, so
// whatever else writes to it — os.Stdout, a library, a child that inherits
// it — lands on stderr. restore points fd 1 back at the client's stdout.
//
// The copy stays open after restore: the transport may still be writing.
func LockStdout() (client *os.File, restore func(), err error) {
	fd, err := unix.FcntlInt(uintptr(unix.Stdout), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	if err := unix.Dup2(unix.Stderr, unix.Stdout); err != nil {
		_ = unix.Close(fd)
		return nil, nil, err
	}
	client = os.NewFile(uintptr(fd), "mcp-stdout")
	return client, func() { _ = unix.Dup2(int(client.Fd()), unix.Stdout) }, nil
}
