// Package errors is how an MCP tool answers when it refuses a call: each
// tool's Err answers a refusals.Refusal as a tool error, its texts for the
// model to read and its detail in the output as refusal, not a JSON-RPC
// error. Logging the call as refused is the caller's.
package errors

import (
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/errors/refusals"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/types"
)

// answer is err as a tool's answer: a refusal a tool error with a text for
// each of its reasons and its detail, for the output to carry; anything else
// the Go error it is.
func answer(err error) (*sdk.CallToolResult, *refusals.Detail, error) {
	var refusal refusals.Refusal
	if !errors.As(err, &refusal) {
		return nil, nil, err
	}
	content := make([]sdk.Content, len(refusal.Reasons))
	for i, reason := range refusal.Reasons {
		content[i] = &sdk.TextContent{Text: reason.Error()}
	}
	return &sdk.CallToolResult{IsError: true, Content: content}, &refusal.Detail, nil
}

// ErrExec is err as terminal_exec's answer.
func ErrExec(err error) (*sdk.CallToolResult, types.Reply, error) {
	r, detail, err := answer(err)
	return r, types.Reply{Refusal: detail}, err
}

// ErrOriginAdd is err as origin_add's answer.
func ErrOriginAdd(err error) (*sdk.CallToolResult, types.Origin, error) {
	r, detail, err := answer(err)
	return r, types.Origin{Refusal: detail}, err
}

// ErrOriginRemove is err as origin_remove's answer.
func ErrOriginRemove(err error) (*sdk.CallToolResult, types.Origin, error) {
	r, detail, err := answer(err)
	return r, types.Origin{Refusal: detail}, err
}

// ErrRead is err as terminal_read's answer.
func ErrRead(err error) (*sdk.CallToolResult, types.Read, error) {
	r, detail, err := answer(err)
	return r, types.Read{Refusal: detail}, err
}

// ErrScreen is err as terminal_screen's answer.
func ErrScreen(err error) (*sdk.CallToolResult, types.Screen, error) {
	r, detail, err := answer(err)
	return r, types.Screen{Refusal: detail}, err
}

// ErrSessions is err as terminal_sessions' answer.
func ErrSessions(err error) (*sdk.CallToolResult, types.Sessions, error) {
	r, detail, err := answer(err)
	return r, types.Sessions{Refusal: detail}, err
}

// ErrWait is err as terminal_wait's answer.
func ErrWait(err error) (*sdk.CallToolResult, types.Reply, error) {
	r, detail, err := answer(err)
	return r, types.Reply{Refusal: detail}, err
}
