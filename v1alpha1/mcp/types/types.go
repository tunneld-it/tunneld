// Package types is what the MCP tools answer: the output each one returns,
// named once so the tool and the tool error that answers for it agree. On a
// tool error, Refusal is why, and the rest is empty.
package types

import (
	v1types "github.com/tunnel-pizza/tunneld/v1/types"
	"github.com/tunnel-pizza/tunneld/v1alpha1/attach"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/errors/refusals"
)

// Reply is what terminal_wait and terminal_exec answer: the output read, and
// how the wait ended.
type Reply struct {
	attach.Output
	// The command line typed, when there was one in a shell that marks its
	// commands: where, whether it still runs, and how it exited.
	*attach.Command
	Matched   bool             `json:"matched" jsonschema:"whether until matched"`
	TimedOut  bool             `json:"timed_out" jsonschema:"whether timeout_ms passed first; neither this nor matched means the program fell quiet"`
	Truncated bool             `json:"truncated" jsonschema:"whether text was cut to its last 64 KiB"`
	Refusal   *refusals.Detail `json:"refusal,omitempty" jsonschema:"why the call was refused, as data, on a tool error and only then"`
}

// Screen is what terminal_screen answers: the terminal's screen as it is.
type Screen struct {
	attach.Screen
	Refusal *refusals.Detail `json:"refusal,omitempty" jsonschema:"why the call was refused, as data, on a tool error and only then"`
}

// Read is what terminal_read answers.
type Read struct {
	attach.Output
	// The command line read from, when cursor is one terminal_exec answered
	// in a shell that marks its commands.
	*attach.Command
	Truncated bool             `json:"truncated" jsonschema:"whether text was cut to its last 64 KiB"`
	Refusal   *refusals.Detail `json:"refusal,omitempty" jsonschema:"why the call was refused, as data, on a tool error and only then"`
}

// Origin is what origin_add and origin_remove answer: the machine, and the
// origin added or removed, as a session.
type Origin struct {
	Machine v1types.Machine  `json:"machine"`
	Session v1types.Session  `json:"session"`
	Refusal *refusals.Detail `json:"refusal,omitempty" jsonschema:"why the call was refused, as data, on a tool error and only then"`
}

// Sessions is what terminal_sessions answers.
type Sessions struct {
	Machine  v1types.Machine   `json:"machine"`
	Sessions []v1types.Session `json:"sessions"`
	Refusal  *refusals.Detail  `json:"refusal,omitempty" jsonschema:"why the call was refused, as data, on a tool error and only then"`
}
