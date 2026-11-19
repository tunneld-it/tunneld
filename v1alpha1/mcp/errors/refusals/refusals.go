// Package refusals is every reason an MCP tool refuses a call: what went
// wrong, and what to do instead, each a text for the model to read, and the
// same as a Detail, for a client that writes its own. A check or a tool's
// run returns one, and the tool's Err in mcp/errors answers with it.
package refusals

import (
	"errors"
	"fmt"

	v1 "github.com/tunnel-pizza/tunneld/v1"
	"github.com/tunnel-pizza/tunneld/v1alpha1/attach"
)

// Refusal is why a call is refused: what went wrong, and what to do instead,
// each an error of its own, and Detail, the same as data.
type Refusal struct {
	Reasons []error
	Detail  Detail
}

func (r Refusal) Error() string   { return errors.Join(r.Reasons...).Error() }
func (r Refusal) Unwrap() []error { return r.Reasons }

// Detail is a refusal as data, for a client that relays the tools under its
// own names and writes its own message: a code that does not change, and
// the values the texts name. It never names a tool.
type Detail struct {
	Code          string         `json:"code" jsonschema:"why, one of: not_implemented, no_terminal, unlisted, not_a_terminal, no_scope, shell_busy, not_a_pattern, stale_cursor, bad_cursor, unreadable, nothing_to_send, bad_attachment, unwritable, unsendable, undiscoverable, unowned, origins_fixed, not_an_origin, serves_by_sign_in, unserved, bad_header, unknown_scheme"`
	URL           string         `json:"url,omitempty" jsonschema:"the terminal's address the call named"`
	Cursor        string         `json:"cursor,omitempty" jsonschema:"the cursor the call passed"`
	CommandCursor string         `json:"command_cursor,omitempty" jsonschema:"where the command running was typed: read from it to wait for its end"`
	Interrupt     []attach.Input `json:"interrupt,omitempty" jsonschema:"input that stops the command running, sent as input alone"`
	Until         string         `json:"until,omitempty" jsonschema:"the pattern the call passed"`
	Path          string         `json:"path,omitempty" jsonschema:"the attachment's path"`
	Origin        string         `json:"origin,omitempty" jsonschema:"the origin, as the call gave it or as the run serves it"`
	Index         *int           `json:"index,omitempty" jsonschema:"the origin's index, its ?n"`
	Scheme        string         `json:"scheme,omitempty" jsonschema:"the origin's scheme, or the scheme the call passed"`
	Scope         string         `json:"scope,omitempty" jsonschema:"the scope the call needs"`
	Header        string         `json:"header,omitempty" jsonschema:"the header's name"`
	Reason        string         `json:"reason,omitempty" jsonschema:"what went wrong, as tunneld found it"`
}

// refusal is a Refusal of detail saying errs, what went wrong, then usage,
// what to do instead, left out when there is nothing to do.
func refusal(detail Detail, usage string, errs ...error) error {
	if usage != "" {
		errs = append(errs, errors.New(usage))
	}
	return Refusal{Reasons: errs, Detail: detail}
}

// NotImplemented is the refusal for tool, which this tunneld offers but
// does not do yet.
func NotImplemented(tool string) error {
	return refusal(Detail{Code: "not_implemented"}, "This run's origins are the ones tunneld was started with; terminal_sessions lists them",
		fmt.Errorf("%s is not implemented yet", tool))
}

// NoTerminal is the refusal for a run with nothing a terminal tool can
// reach: no program or container, which until one is added is a run with no
// origins at all.
func NoTerminal() error {
	return refusal(Detail{Code: "no_terminal"}, "A terminal is there once tunneld runs a program or attaches a container; terminal_sessions with scheme http lists the web addresses it serves, if any",
		errors.New("This run serves no terminal: it has no program (exec) or container (attach) to type into"))
}

// Unlisted is the refusal for url, an address that names no origin of the
// run, err saying how.
func Unlisted(url string, err error) error {
	return refusal(Detail{Code: "unlisted", URL: url, Reason: err.Error()}, "terminal_sessions lists the terminals", err)
}

// NotATerminal is the refusal for url, which names origin, an origin the run
// dials rather than serves with a terminal.
func NotATerminal(url string, origin v1.Origin) error {
	return refusal(Detail{Code: "not_a_terminal", URL: url, Index: &origin.Index, Scheme: string(origin.Scheme)}, "terminal_sessions lists the terminals",
		fmt.Errorf("%s is not a terminal: origin %d is %s", url, origin.Index, origin.Scheme))
}

// NoScope is the refusal for a sign-in whose token does not hold scope,
// which tool needs.
func NoScope(tool, scope string) error {
	return refusal(Detail{Code: "no_scope", Scope: scope}, fmt.Sprintf("%s needs the %s scope", tool, scope),
		fmt.Errorf("This sign-in can't call %s", tool))
}

// Busy is the refusal for a command line sent to the terminal at url while
// the shell there runs the command typed at cursor.
func Busy(url, cursor string) error {
	return refusal(Detail{Code: "shell_busy", URL: url, CommandCursor: cursor, Interrupt: []attach.Input{{Key: "Ctrl-C"}}}, fmt.Sprintf(`Read it with terminal_wait, url %s and cursor %s, until it ends; or stop it with terminal_exec and input [{"key":"Ctrl-C"}], which is sent while a command runs`, url, cursor),
		fmt.Errorf("The shell at %s is running a command, so a command line now would be typed into it", url))
}

// NotAPattern is the refusal for until, which does not compile, err saying
// why.
func NotAPattern(until string, err error) error {
	return refusal(Detail{Code: "not_a_pattern", Until: until, Reason: err.Error()}, "until takes RE2 syntax: \\s is ASCII only, \\p{Zs} matches any space",
		fmt.Errorf("until is not a regular expression: %w", err))
}

// Unreadable is the refusal for a read from cursor that failed: a cursor not
// the terminal's own, from another terminal or before tunneld started again,
// or one no terminal gave; anything else as it is.
func Unreadable(cursor string, err error) error {
	switch {
	case errors.Is(err, attach.ErrStaleCursor):
		return refusal(Detail{Code: "stale_cursor", Cursor: cursor}, "Leave cursor out to read on from where this sign-in last stopped",
			errors.New("That cursor is not this terminal's: it came from another terminal, or from before tunneld started again, so the output it points into is gone"))
	case errors.Is(err, attach.ErrBadCursor):
		return refusal(Detail{Code: "bad_cursor", Cursor: cursor}, "pass back one a terminal tool answered, or leave it out",
			errors.New("That is not a cursor"))
	}
	return refusal(Detail{Code: "unreadable", Cursor: cursor, Reason: err.Error()}, "", err)
}

// NothingToSend is the refusal for a call with no input steps, no command
// and no attachments.
func NothingToSend() error {
	return refusal(Detail{Code: "nothing_to_send"}, "send input steps, a command, attachments, or a mix", errors.New("Nothing to send"))
}

// BadAttachment is the refusal for the attachment at path, checked before
// anything was written, err saying which and why; path is empty when what
// is wrong is their size.
func BadAttachment(path string, err error) error {
	return refusal(Detail{Code: "bad_attachment", Path: path, Reason: err.Error()}, "each attachment is an absolute path, a data: URI, and optionally an octal mode, 1 MiB in all", err)
}

// Unwritable is the refusal for attachments this machine would not take,
// err saying why; nothing was typed.
func Unwritable(err error) error {
	return refusal(Detail{Code: "unwritable", Reason: err.Error()}, "nothing was typed; pick a path tunneld's user can write", fmt.Errorf("Could not write the attachments: %w", err))
}

// Unsendable is the refusal for input the terminal would not send, err
// saying which step and why: one that is both text and a key or neither, or
// a key it does not know.
func Unsendable(err error) error {
	return refusal(Detail{Code: "unsendable", Reason: err.Error()}, "each step is exactly one of text or key", err)
}

// Undiscoverable is the refusal for tool, which lists what a tunnel serves,
// to a caller an unowned public tunnel let in: such a tunnel is reached by
// the address of a terminal its caller was given, never found.
func Undiscoverable(tool string) error {
	return refusal(Detail{Code: "undiscoverable"}, "Act on a terminal you were given the address of: terminal_exec, terminal_read, terminal_wait or terminal_screen with https://hostname/, or https://hostname/?n for origin n",
		fmt.Errorf("%s does not list a tunnel nobody owns", tool))
}

// Unowned is the refusal for tool, which changes what a tunnel serves, to a
// caller an unowned public tunnel let in: that tunnel's terminals are open
// to anyone with an address, its origins to nobody but the one who ran it.
func Unowned(tool string) error {
	return refusal(Detail{Code: "unowned"}, "Only the client that started its tunneld, over stdio, or the tunnel's secret can",
		fmt.Errorf("%s can't change a tunnel nobody owns", tool))
}

// Fixed is the refusal for tool on a run whose origins cannot change: one
// given none to change.
func Fixed(tool string) error {
	return refusal(Detail{Code: "origins_fixed"}, "", fmt.Errorf("This run's origins are fixed: %s can't change them", tool))
}

// NotAnOrigin is the refusal for word, which reads as no origin at all.
func NotAnOrigin(word string) error {
	return refusal(Detail{Code: "not_an_origin", Origin: word}, "origin is a web address, such as https://api.example.com",
		fmt.Errorf("%q is not an origin", word))
}

// ServesBySignIn is the refusal for origin, a program or a container, asked
// of origin_add by a sign-in.
func ServesBySignIn(origin v1.Origin) error {
	return refusal(Detail{Code: "serves_by_sign_in", Origin: origin.Label(), Scheme: string(origin.Scheme)}, "a sign-in adds web addresses; the client that started this tunneld, over stdio, or the tunnel's secret starts a program or attaches a container",
		fmt.Errorf("This sign-in can't add %s: it is %s", origin.Label(), origin.Scheme))
}

// Unserved is the refusal for origin, a program or a container that could
// not be stood up, err saying why.
func Unserved(origin v1.Origin, err error) error {
	return refusal(Detail{Code: "unserved", Origin: origin.Label(), Reason: err.Error()}, "nothing was added; check the program is on this machine, or the container is running, and try again",
		fmt.Errorf("Could not serve %s: %w", origin.Label(), err))
}

// BadHeader is the refusal for header name, err saying why.
func BadHeader(name string, err error) error {
	return refusal(Detail{Code: "bad_header", Header: name, Reason: err.Error()}, "headers are names and values a request could carry; Host and the headers a connection is managed by are tunneld's to set",
		fmt.Errorf("header %q: %w", name, err))
}

// UnknownScheme is the refusal for scheme, which no origin can have.
func UnknownScheme(scheme v1.Scheme) error {
	return refusal(Detail{Code: "unknown_scheme", Scheme: string(scheme)}, "scheme must be exec, attach or http", fmt.Errorf("%q is not a scheme", scheme))
}
