// Package stdioutil holds this process's standard streams for a protocol
// spoken over them, such as an MCP client's JSON-RPC on stdout.
package stdioutil

import (
	"os"

	"golang.org/x/sys/windows"
)

// LockStdout gives an MCP run's client the process's stdout alone. os.Stdout
// and the process's standard output handle both become stderr, so whatever
// else writes there — Go code, a library, a child that inherits the handle —
// lands on stderr, and the transport keeps the original. restore puts both
// back.
func LockStdout() (client *os.File, restore func(), err error) {
	client = os.Stdout
	if err := windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(os.Stderr.Fd())); err != nil {
		return nil, nil, err
	}
	os.Stdout = os.Stderr
	return client, func() {
		os.Stdout = client
		_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(client.Fd()))
	}, nil
}
