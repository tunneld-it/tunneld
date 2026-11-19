package errors

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/errors/refusals"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/types"
)

// texts is r's texts, failing unless r is a tool error carrying only text.
func texts(t *testing.T, r *sdk.CallToolResult) []string {
	t.Helper()
	if r == nil || !r.IsError || len(r.Content) == 0 {
		t.Fatalf("result = %+v, want a tool error with text", r)
	}
	out := make([]string, len(r.Content))
	for i, c := range r.Content {
		out[i] = c.(*sdk.TextContent).Text
	}
	return out
}

// noTerminal is what refusals.NoTerminal says.
var noTerminal = []string{"This run serves no terminal: it has no program (exec) or container (attach) to type into", "A terminal is there once tunneld runs a program or attaches a container; terminal_sessions with scheme http lists the web addresses it serves, if any"}

// TestWrapped pins that a refusal wrapped by a check still answers as one.
func TestWrapped(t *testing.T) {
	r, _, err := ErrWait(fmt.Errorf("checking: %w", refusals.NoTerminal()))
	if err != nil || !slices.Equal(texts(t, r), noTerminal) {
		t.Errorf("ErrWait(a wrapped refusal) = %+v, %v; want its text, no Go error", r, err)
	}
}

// TestAnswers pins each tool's Err: a refusal is a tool error, its texts in
// the content and its detail the output's refusal, the rest of the output
// empty, and no Go error; any other error is the Go error it is, with the
// empty output.
func TestAnswers(t *testing.T) {
	refusal, other := refusals.NoTerminal(), fmt.Errorf("broke")
	check := func(name string, r *sdk.CallToolResult, detail *refusals.Detail, empty bool, err error, wantErr error) {
		t.Helper()
		if wantErr == nil {
			if err != nil || !empty || detail == nil || detail.Code != "no_terminal" || !slices.Equal(texts(t, r), noTerminal) {
				t.Errorf("%s(refusal) = %+v, refusal %+v, rest empty %v, %v; want the tool error, its detail, no Go error", name, r, detail, empty, err)
			}
			return
		}
		if r != nil || err != wantErr || detail != nil || !empty {
			t.Errorf("%s(other) = %+v, refusal %+v, %v; want no result, the empty output and the Go error", name, r, detail, err)
		}
	}
	for _, err := range []error{refusal, other} {
		var want error
		if err == other {
			want = other
		}
		r, reply, e := ErrExec(err)
		check("ErrExec", r, reply.Refusal, reply == types.Reply{Refusal: reply.Refusal}, e, want)
		r, added, e := ErrOriginAdd(err)
		check("ErrOriginAdd", r, added.Refusal, reflect.DeepEqual(added, types.Origin{Refusal: added.Refusal}), e, want)
		r, removed, e := ErrOriginRemove(err)
		check("ErrOriginRemove", r, removed.Refusal, reflect.DeepEqual(removed, types.Origin{Refusal: removed.Refusal}), e, want)
		r, read, e := ErrRead(err)
		check("ErrRead", r, read.Refusal, read == types.Read{Refusal: read.Refusal}, e, want)
		r, screen, e := ErrScreen(err)
		check("ErrScreen", r, screen.Refusal, screen.Rows == nil, e, want)
		r, sessions, e := ErrSessions(err)
		check("ErrSessions", r, sessions.Refusal, sessions.Sessions == nil, e, want)
		r, reply, e = ErrWait(err)
		check("ErrWait", r, reply.Refusal, reply == types.Reply{Refusal: reply.Refusal}, e, want)
	}
}
