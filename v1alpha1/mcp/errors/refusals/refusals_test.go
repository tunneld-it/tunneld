package refusals

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	v1 "github.com/tunnel-pizza/tunneld/v1"
	"github.com/tunnel-pizza/tunneld/v1alpha1/attach"
)

// texts is what err's reasons say, failing unless err is a Refusal.
func texts(t *testing.T, err error) []string {
	t.Helper()
	var r Refusal
	if !errors.As(err, &r) {
		t.Fatalf("%v is not a refusal", err)
	}
	out := make([]string, len(r.Reasons))
	for i, reason := range r.Reasons {
		out[i] = reason.Error()
	}
	return out
}

// TestRefusal pins a refusal: what went wrong, then what to do, as a Go
// error in that order, the reason still found by errors.Is, and no usage
// left out.
func TestRefusal(t *testing.T) {
	wrong := errors.New("what went wrong")
	err := refusal(Detail{Code: "c"}, "what to do instead", wrong)
	if got := err.Error(); got != "what went wrong\nwhat to do instead" {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(err, wrong) {
		t.Errorf("errors.Is does not find the reason in %v", err)
	}
	if got := texts(t, refusal(Detail{Code: "c"}, "", wrong)); len(got) != 1 {
		t.Errorf("refusal with no usage says %q, want the reason alone", got)
	}
	if bad := fmt.Errorf("missing closing )"); !errors.Is(NotAPattern("(", bad), bad) {
		t.Error("NotAPattern hides the error it wraps")
	}
}

// TestSays pins what each refusal tells the model: what went wrong, then
// what to do instead where there is something, a text each.
func TestSays(t *testing.T) {
	shell, _ := url.Parse("http://localhost:3000")
	for name, tc := range map[string]struct {
		err  error
		says []string
	}{
		"NotImplemented": {NotImplemented("origin_add"), []string{"origin_add is not implemented yet", "This run's origins are the ones tunneld was started with; terminal_sessions lists them"}},
		"NoTerminal":     {NoTerminal(), []string{"This run serves no terminal: it has no program (exec) or container (attach) to type into", "A terminal is there once tunneld runs a program or attaches a container; terminal_sessions with scheme http lists the web addresses it serves, if any"}},
		"Unlisted":       {Unlisted("https://h/?5", fmt.Errorf("names origin 5")), []string{"names origin 5", "terminal_sessions lists the terminals"}},
		"NotATerminal":   {NotATerminal("https://0tabc.tunneld.domain/", v1.Origin{Index: 0, URI: *shell, Scheme: v1.HttpScheme}), []string{"https://0tabc.tunneld.domain/ is not a terminal: origin 0 is http", "terminal_sessions lists the terminals"}},
		"NoScope":        {NoScope("a_tool", "tunnel:exec"), []string{"This sign-in can't call a_tool", "a_tool needs the tunnel:exec scope"}},
		"Busy":           {Busy("https://h/", "@7"), []string{"The shell at https://h/ is running a command, so a command line now would be typed into it", `Read it with terminal_wait, url https://h/ and cursor @7, until it ends; or stop it with terminal_exec and input [{"key":"Ctrl-C"}], which is sent while a command runs`}},
		"NotAPattern":    {NotAPattern("(", fmt.Errorf("missing closing )")), []string{"until is not a regular expression: missing closing )", `until takes RE2 syntax: \s is ASCII only, \p{Zs} matches any space`}},
		"NothingToSend":  {NothingToSend(), []string{"Nothing to send", "send input steps, a command, attachments, or a mix"}},
		"BadAttachment":  {BadAttachment("/tmp/a", fmt.Errorf("attachment 1: not a data: URI")), []string{"attachment 1: not a data: URI", "each attachment is an absolute path, a data: URI, and optionally an octal mode, 1 MiB in all"}},
		"Unwritable":     {Unwritable(fmt.Errorf("permission denied")), []string{"Could not write the attachments: permission denied", "nothing was typed; pick a path tunneld's user can write"}},
		"Unsendable":     {Unsendable(fmt.Errorf("%w %q: step 2", attach.ErrUnknownKey, "Hyper")), []string{`attach: unknown key "Hyper": step 2`, "each step is exactly one of text or key"}},
		"Undiscoverable": {Undiscoverable("terminal_sessions"), []string{"terminal_sessions does not list a tunnel nobody owns", "Act on a terminal you were given the address of: terminal_exec, terminal_read, terminal_wait or terminal_screen with https://hostname/, or https://hostname/?n for origin n"}},
		"Unowned":        {Unowned("origin_add"), []string{"origin_add can't change a tunnel nobody owns", "Only the client that started its tunneld, over stdio, or the tunnel's secret can"}},
		"Fixed":          {Fixed("origin_add"), []string{"This run's origins are fixed: origin_add can't change them"}},
		"NotAnOrigin":    {NotAnOrigin(""), []string{`"" is not an origin`, "origin is a web address, such as https://api.example.com"}},
		"ServesBySignIn": {ServesBySignIn(v1.Origin{URI: url.URL{Scheme: "exec", Path: "/bin/bash"}, Scheme: v1.ExecScheme}), []string{"This sign-in can't add exec:///bin/bash: it is exec", "a sign-in adds web addresses; the client that started this tunneld, over stdio, or the tunnel's secret starts a program or attaches a container"}},
		"Unserved":       {Unserved(v1.Origin{URI: url.URL{Scheme: "attach", Host: "dockerd", Path: "/web"}, Scheme: v1.AttachScheme}, fmt.Errorf("no such container")), []string{"Could not serve attach://dockerd/web: no such container", "nothing was added; check the program is on this machine, or the container is running, and try again"}},
		"BadHeader":      {BadHeader("Host", fmt.Errorf("tunneld sets it")), []string{`header "Host": tunneld sets it`, "headers are names and values a request could carry; Host and the headers a connection is managed by are tunneld's to set"}},
		"UnknownScheme":  {UnknownScheme("ssh"), []string{`"ssh" is not a scheme`, "scheme must be exec, attach or http"}},
	} {
		if got := texts(t, tc.err); !slices.Equal(got, tc.says) {
			t.Errorf("%s says %q, want %q", name, got, tc.says)
		}
	}
}

// TestUnreadable pins the refusal for a read that failed: a cursor it cannot
// use said with what to do, anything else as it is.
func TestUnreadable(t *testing.T) {
	for err, says := range map[error]int{
		attach.ErrStaleCursor:           2,
		attach.ErrBadCursor:             2,
		fmt.Errorf("the session ended"): 1,
	} {
		if got := texts(t, Unreadable("@1", err)); len(got) != says {
			t.Errorf("Unreadable(%v) says %q, want %d texts", err, got, says)
		}
	}
	if got := texts(t, Unreadable("@1", fmt.Errorf("the session ended"))); got[0] != "the session ended" {
		t.Errorf("Unreadable(other) says %q, want it as it is", got)
	}
}

// TestDetail pins each refusal as data: its code, and the values its texts
// name, no tool's name among them.
func TestDetail(t *testing.T) {
	shell, _ := url.Parse("http://localhost:3000")
	zero := 0
	for name, tc := range map[string]struct {
		err  error
		want Detail
	}{
		"NotImplemented": {NotImplemented("origin_remove"), Detail{Code: "not_implemented"}},
		"NoTerminal":     {NoTerminal(), Detail{Code: "no_terminal"}},
		"Unlisted":       {Unlisted("https://h/?5", fmt.Errorf("names origin 5")), Detail{Code: "unlisted", URL: "https://h/?5", Reason: "names origin 5"}},
		"NotATerminal":   {NotATerminal("https://h/", v1.Origin{Index: 0, URI: *shell, Scheme: v1.HttpScheme}), Detail{Code: "not_a_terminal", URL: "https://h/", Index: &zero, Scheme: "http"}},
		"NoScope":        {NoScope("terminal_exec", "tunnel:exec"), Detail{Code: "no_scope", Scope: "tunnel:exec"}},
		"Busy":           {Busy("https://h/", "@7"), Detail{Code: "shell_busy", URL: "https://h/", CommandCursor: "@7", Interrupt: []attach.Input{{Key: "Ctrl-C"}}}},
		"NotAPattern":    {NotAPattern("(", fmt.Errorf("missing closing )")), Detail{Code: "not_a_pattern", Until: "(", Reason: "missing closing )"}},
		"stale":          {Unreadable("@1", fmt.Errorf("read: %w", attach.ErrStaleCursor)), Detail{Code: "stale_cursor", Cursor: "@1"}},
		"bad":            {Unreadable("x", attach.ErrBadCursor), Detail{Code: "bad_cursor", Cursor: "x"}},
		"Unreadable":     {Unreadable("@1", fmt.Errorf("the session ended")), Detail{Code: "unreadable", Cursor: "@1", Reason: "the session ended"}},
		"NothingToSend":  {NothingToSend(), Detail{Code: "nothing_to_send"}},
		"BadAttachment":  {BadAttachment("/tmp/a", fmt.Errorf("attachment 1: not a data: URI")), Detail{Code: "bad_attachment", Path: "/tmp/a", Reason: "attachment 1: not a data: URI"}},
		"Unwritable":     {Unwritable(fmt.Errorf("permission denied")), Detail{Code: "unwritable", Reason: "permission denied"}},
		"Unsendable":     {Unsendable(fmt.Errorf("step 2: no key Hyper")), Detail{Code: "unsendable", Reason: "step 2: no key Hyper"}},
		"Undiscoverable": {Undiscoverable("terminal_sessions"), Detail{Code: "undiscoverable"}},
		"Unowned":        {Unowned("origin_add"), Detail{Code: "unowned"}},
		"Fixed":          {Fixed("origin_add"), Detail{Code: "origins_fixed"}},
		"NotAnOrigin":    {NotAnOrigin("ftp:"), Detail{Code: "not_an_origin", Origin: "ftp:"}},
		"ServesBySignIn": {ServesBySignIn(v1.Origin{Index: 2, URI: url.URL{Scheme: "exec", Path: "/bin/bash"}, Scheme: v1.ExecScheme}), Detail{Code: "serves_by_sign_in", Origin: "exec:///bin/bash", Scheme: "exec"}},
		"Unserved":       {Unserved(v1.Origin{URI: url.URL{Scheme: "attach", Host: "dockerd", Path: "/web"}, Scheme: v1.AttachScheme}, fmt.Errorf("no such container")), Detail{Code: "unserved", Origin: "attach://dockerd/web", Reason: "no such container"}},
		"BadHeader":      {BadHeader("Host", fmt.Errorf("tunneld sets it")), Detail{Code: "bad_header", Header: "Host", Reason: "tunneld sets it"}},
		"UnknownScheme":  {UnknownScheme("ssh"), Detail{Code: "unknown_scheme", Scheme: "ssh"}},
	} {
		var r Refusal
		if !errors.As(tc.err, &r) {
			t.Fatalf("%s is not a refusal", name)
		}
		if !reflect.DeepEqual(r.Detail, tc.want) {
			t.Errorf("%s detail = %+v, want %+v", name, r.Detail, tc.want)
		}
		if got := fmt.Sprintf("%+v", r.Detail); strings.Contains(got, "terminal_") || strings.Contains(got, "origin_") {
			t.Errorf("%s detail %s names a tool", name, got)
		}
	}
}
