package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	v1 "github.com/tunnel-pizza/tunneld/v1"
	"github.com/tunnel-pizza/tunneld/v1alpha1/attach"
	"github.com/tunnel-pizza/tunneld/v1alpha1/attach/shell"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/tool/tooltest"
	"github.com/tunnel-pizza/tunneld/v1alpha1/origins"
)

// TestWithStdio pins the streams: both given serve the server on them, and
// closing its writer leaves the one given open; either missing serves none.
func TestWithStdio(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

	m := v1.Apply(&McpImpl{}, WithStdio(r, w))
	got, ok := m.stdio.(*sdk.IOTransport)
	if !ok {
		t.Fatalf("stdio = %#v, want an IOTransport", m.stdio)
	}
	if err := got.Writer.Close(); err != nil {
		t.Errorf("closing the writer = %v, want it to leave w open", err)
	}
	if _, err := io.WriteString(w, "open"); err != nil {
		t.Errorf("w after the writer closed: %v", err)
	}

	for name, opt := range map[string]Option{
		"no reader": WithStdio(nil, w),
		"no writer": WithStdio(r, nil),
	} {
		if m := v1.Apply(&McpImpl{}, opt); m.stdio != nil {
			t.Errorf("%s: stdio = %#v, want none", name, m.stdio)
		}
	}
}

// connect stands m's server up over an in-memory transport and returns a
// client session on it.
func connect(t *testing.T, m *McpImpl) *sdk.ClientSession {
	t.Helper()
	t.Cleanup(func() { _ = m.Close() })
	ct, st := sdk.NewInMemoryTransports()
	ss, err := m.server.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestNew pins the options: the origins and the served origins are kept, a
// log replaces the discarding default, and a nil log keeps it.
func TestNew(t *testing.T) {
	all := tooltest.Origins(t, "http://localhost:3000", "exec:///bin/sh")
	served := tooltest.Attached{}
	log := slog.New(slog.DiscardHandler)
	for _, tc := range []struct {
		name     string
		opts     []Option
		origins  v1.Origins
		attached attach.Attached
		log      *slog.Logger
	}{
		{"defaults", nil, nil, nil, nil},
		{"origins", []Option{WithOrigins(all)}, all, nil, nil},
		{"attached", []Option{WithAttached(served)}, nil, served, nil},
		{"log", []Option{WithLog(log)}, nil, nil, log},
		{"nil log keeps the default", []Option{WithLog(nil)}, nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(tc.opts...)
			if m.origins != tc.origins {
				t.Errorf("origins = %v, want the ones given, %v", m.origins, tc.origins)
			}
			if (m.attached == nil) != (tc.attached == nil) {
				t.Errorf("attached = %v, want the one given, %v", m.attached, tc.attached)
			}
			if m.log == nil {
				t.Error("log = nil, want the discarding default")
			}
			if tc.log != nil && m.log != tc.log {
				t.Error("log is not the one given")
			}
		})
	}
}

// TestCloseTwice pins the closer: the context's end and a caller's own Close
// may both close it.
func TestCloseTwice(t *testing.T) {
	m := New()
	for range 2 {
		if err := m.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

// TestWithContext pins the run's context as the closer: once it is done the
// server is closed, with nothing left for a caller to defer.
func TestWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	m := New(WithContext(ctx))
	cancel()
	if m.stop == nil || m.stop() {
		t.Error("the context is done and the server was not closed")
	}
}

// TestWithEnded pins when the stdio session's end is reported: a client
// that spoke closing stdin is; the server being closed first is not, and
// neither is a stdin that closes with nothing sent, as /dev/null does.
func TestWithEnded(t *testing.T) {
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}` + "\n"
	for _, tc := range []struct {
		name  string
		sent  string
		close bool
		want  bool
	}{
		{"the client closes stdin", initialize, false, true},
		{"the server is closed first", initialize, true, false},
		{"nothing was ever sent", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe: %v", err)
			}
			t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
			ended := make(chan struct{})
			m := New(WithStdio(r, io.Discard), WithEnded(func() { close(ended) }))
			if tc.close {
				_ = m.Close()
			}
			_, _ = io.WriteString(w, tc.sent)
			_ = w.Close()

			wait := 5 * time.Second
			if !tc.want {
				wait = 200 * time.Millisecond
			}
			select {
			case <-ended:
				if !tc.want {
					t.Error("ended was called, want the run left serving")
				}
			case <-time.After(wait):
				if tc.want {
					t.Error("ended was not called once stdin closed")
				}
			}
			_ = m.Close()
		})
	}
}

// TestServeAcceptsAForwardedHost pins the loopback: the router listens on
// 127.0.0.1 and the tunnel forwards the public hostname as Host, which the
// SDK calls DNS rebinding and refuses unless told the secret is the guard.
func TestServeAcceptsAForwardedHost(t *testing.T) {
	m := New()
	defer m.Close()
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
	req.Host = "abc.tunnel.pizza"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("initialize with a forwarded Host = %d %s, want 200", resp.StatusCode, b)
	}
}

// TestATerminalOverMcp drives a real /bin/sh through the server, as an agent
// does: terminal_sessions finds it, terminal_exec types a command and
// presses Enter, terminal_wait from the offset it answered reads the reply,
// and terminal_screen shows it.
func TestATerminalOverMcp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	shown := tooltest.Origins(t, "http://localhost:3000", "exec:///bin/sh")
	attached, err := attach.New(attach.WithTargets(shell.New())).Attach(t.Context(), shown, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer func() { _ = attached.Close() }()
	cs := connect(t, New(WithOrigins(shown), WithAttached(attached)))

	call := func(name, args string) *sdk.CallToolResult {
		t.Helper()
		var a map[string]any
		_ = json.Unmarshal([]byte(args), &a)
		res, err := cs.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: a})
		if err != nil || res.IsError {
			t.Fatalf("%s(%s) = %+v, %v", name, args, res, err)
		}
		return res
	}
	if listed, _ := json.Marshal(call("terminal_sessions", `{"scheme":"exec"}`).StructuredContent); !strings.Contains(string(listed), `"index":1`) {
		t.Fatalf("terminal_sessions = %s, want the shell at index 1", listed)
	}
	type reply struct {
		Text    string
		Cursor  string
		Matched bool
	}
	answer := func(res *sdk.CallToolResult) reply {
		t.Helper()
		raw, _ := json.Marshal(res.StructuredContent)
		var r reply
		_ = json.Unmarshal(raw, &r)
		return r
	}
	// The shell's prompt first, as an agent waits for it: typed before it,
	// the input is echoed ahead of the prompt.
	if prompt := answer(call("terminal_wait", `{"url":"https://0tabc.tunneld.domain/?1","until":"\\$ $"}`)); !prompt.Matched {
		t.Fatalf("terminal_wait for the prompt = %+v, want it matched", prompt)
	}
	// terminal_exec waits for the reply itself.
	first := answer(call("terminal_exec", `{"url":"https://0tabc.tunneld.domain/?1","input":[{"text":"echo $((6*7))"},{"key":"Enter"}],"until":"(?m)^42$"}`))
	if !first.Matched || !strings.Contains(first.Text, "echo $((6*7))\n42\n") || first.Cursor == "" {
		t.Fatalf("terminal_exec = %+v, want the command echoed and 42 under it, matched, and a cursor", first)
	}
	// Sent without waiting, then read on from where this caller stopped.
	call("terminal_exec", `{"url":"https://0tabc.tunneld.domain/?1","input":[{"text":"echo second"},{"key":"Enter"}],"idle_ms":0}`)
	second := answer(call("terminal_wait", `{"url":"https://0tabc.tunneld.domain/?1","until":"(?m)^second$"}`))
	if !second.Matched || !strings.Contains(second.Text, "second\n") || strings.Contains(second.Text, "42") {
		t.Fatalf("terminal_wait = %+v, want the second reply alone, matched", second)
	}
	// A cursor reads from where it says, whatever this caller's position.
	again := answer(call("terminal_read", `{"url":"https://0tabc.tunneld.domain/?1","cursor":"`+first.Cursor+`"}`))
	if !strings.Contains(again.Text, "echo second\nsecond\n") {
		t.Fatalf("terminal_read from the first answer's cursor = %+v, want the second command and its reply", again)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		var screen attach.Screen
		raw, _ := json.Marshal(call("terminal_screen", `{"url":"https://0tabc.tunneld.domain/?1"}`).StructuredContent)
		_ = json.Unmarshal(raw, &screen)
		if slices.Contains(screen.Rows, "42") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("screen = %q, want a row reading 42", screen.Rows)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestAShellAddedOverMcp drives what an agent does on a run with no
// terminal: origin_add a real /bin/sh, then type into it at the index it
// answered, as terminal_exec does with any other.
func TestAShellAddedOverMcp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	list := origins.New(origins.WithOrigins(tooltest.Origins(t, "http://localhost:3000").All()...))
	attached, err := attach.New(attach.WithTargets(shell.New())).Attach(t.Context(), list, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer func() { _ = attached.Close() }()
	cs := connect(t, New(WithOrigins(list), WithAttached(attached)))

	res, err := cs.CallTool(t.Context(), &sdk.CallToolParams{Name: "origin_add", Arguments: map[string]any{"origin": "exec:///bin/sh"}})
	if err != nil || res.IsError {
		t.Fatalf("origin_add = %+v, %v", res, err)
	}
	if raw, _ := json.Marshal(res.StructuredContent); !strings.Contains(string(raw), `"index":1`) {
		t.Fatalf("origin_add = %s, want the shell at index 1", raw)
	}
	res, err = cs.CallTool(t.Context(), &sdk.CallToolParams{Name: "terminal_exec", Arguments: map[string]any{
		"url":   "https://0tabc.tunneld.domain/?1",
		"input": []map[string]any{{"text": "echo $((6*7))"}, {"key": "Enter"}},
		"until": "(?m)^42$",
	}})
	if err != nil || res.IsError {
		t.Fatalf("terminal_exec on the added shell = %+v, %v", res, err)
	}
	if raw, _ := json.Marshal(res.StructuredContent); !strings.Contains(string(raw), `"matched":true`) {
		t.Errorf("terminal_exec on the added shell = %s, want 42 matched", raw)
	}
}

// TestShellsMarkTheirCommands drives each shell on this machine through the
// tools, as an agent does: a command line answers how it exited; one still
// running answers running and its cursor, a second line is refused while it
// runs and Ctrl-C is not, and a wait or a read from that cursor answers how it
// ended.
func TestShellsMarkTheirCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no shells to mark")
	}
	home := t.TempDir()
	for name, body := range map[string]string{".bashrc": "rc_read=bashrc\n", ".zshrc": "rc_read=zshrc\n", ".shrc": "rc_read=shrc\n"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", "")
	t.Setenv("ENV", filepath.Join(home, ".shrc"))
	for _, name := range []string{"bash", "zsh", "dash", "ksh", "mksh", "sh", "fish"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			list := origins.New(origins.WithOrigins(tooltest.Origins(t, "exec://"+path).All()...))
			attached, err := attach.New(attach.WithTargets(shell.New())).Attach(t.Context(), list, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatalf("Attach: %v", err)
			}
			defer func() { _ = attached.Close() }()
			cs := connect(t, New(WithOrigins(list), WithAttached(attached)))
			const url = "https://0tabc.tunneld.domain/"
			call := func(tool string, args map[string]any) map[string]any {
				t.Helper()
				res, err := cs.CallTool(t.Context(), &sdk.CallToolParams{Name: tool, Arguments: args})
				if err != nil {
					t.Fatalf("%s: %v", tool, err)
				}
				raw, _ := json.Marshal(res.StructuredContent)
				var out map[string]any
				_ = json.Unmarshal(raw, &out)
				if res.IsError {
					out = map[string]any{"error": res.Content[0].(*sdk.TextContent).Text}
				}
				return out
			}
			// The first prompt, which the shell marks before it draws it.
			term, _ := attached.Terminal(0)
			for deadline := time.Now().Add(10 * time.Second); ; {
				if out, _ := term.Read("probe", ""); strings.TrimSpace(out.Text) != "" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("no prompt")
				}
				time.Sleep(50 * time.Millisecond)
			}

			own := call("terminal_exec", map[string]any{"url": url, "command": "echo read:$rc_read", "timeout_ms": 5000})
			if text := fmt.Sprint(own["text"]); name != "fish" && !regexp.MustCompile(`(?m)^read:(bash|zsh|sh)rc$`).MatchString(text) {
				t.Errorf("echo read:$rc_read = %q, want the shell's own rc read first", text)
			}

			got := call("terminal_exec", map[string]any{"url": url, "command": "sh -c 'exit 7'", "timeout_ms": 5000})
			if got["exit_code"] != float64(7) || got["running"] != false || got["command_cursor"] == nil {
				t.Fatalf("sh -c 'exit 7' = %v, want exit_code 7, not running, a command_cursor", got)
			}

			slow := call("terminal_exec", map[string]any{"url": url, "command": "sleep 2", "timeout_ms": 300})
			if slow["running"] != true || slow["exit_code"] != nil || slow["timed_out"] != true {
				t.Fatalf("sleep 2 after 300ms = %v, want running, no exit_code, timed out", slow)
			}
			if refused := call("terminal_exec", map[string]any{"url": url, "command": "echo no"}); !strings.Contains(fmt.Sprint(refused["error"]), "is running a command") {
				t.Errorf("a command line while sleep runs = %v, want it refused", refused)
			}
			cursor := slow["command_cursor"]
			waited := call("terminal_wait", map[string]any{"url": url, "cursor": cursor, "timeout_ms": 5000})
			if waited["exit_code"] != float64(0) || waited["running"] != false || waited["timed_out"] != false {
				t.Errorf("terminal_wait from the command's cursor = %v, want it ended 0", waited)
			}
			read := call("terminal_read", map[string]any{"url": url, "cursor": cursor})
			if read["exit_code"] != float64(0) || read["running"] != false {
				t.Errorf("terminal_read from the command's cursor = %v, want it ended 0", read)
			}

			stuck := call("terminal_exec", map[string]any{"url": url, "command": "sleep 30", "timeout_ms": 300})
			if stuck["running"] != true {
				t.Fatalf("sleep 30 = %v, want running", stuck)
			}
			if stopped := call("terminal_exec", map[string]any{"url": url, "input": []map[string]any{{"key": "Ctrl-C"}}, "timeout_ms": 3000, "until": "\\S"}); stopped["error"] != nil {
				t.Errorf("Ctrl-C while running = %v, want it sent", stopped)
			}
			ended := call("terminal_wait", map[string]any{"url": url, "cursor": stuck["command_cursor"], "timeout_ms": 5000})
			if ended["running"] != false || ended["exit_code"] == nil || ended["exit_code"] == float64(0) {
				t.Errorf("sleep 30 after Ctrl-C = %v, want it ended, not 0", ended)
			}
		})
	}
}

// TestOriginToolsWithoutAToken pins who reaches origin_add and origin_remove
// without a token: a client on the server's own connection, as stdio is, and
// one through the HTTP handler with no token, as a call over the tunnel with
// its secret is, both get as far as the tool: origin_add adds the origin,
// origin_remove is not implemented yet.
func TestOriginToolsWithoutAToken(t *testing.T) {
	m := New(WithOrigins(tooltest.Origins(t, "exec:///bin/sh")))
	cs := connect(t, m)
	for name, tc := range map[string]struct {
		args map[string]any
		says string
	}{
		"origin_add":    {map[string]any{"origin": ":3000"}, `"ref":"localhost:3000"`},
		"origin_remove": {map[string]any{"origin": "1"}, "origin_remove is not implemented yet"},
	} {
		r, err := cs.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: tc.args})
		if err != nil || !strings.Contains(r.Content[0].(*sdk.TextContent).Text, tc.says) {
			t.Errorf("%s on the server's own connection = %+v, %v; want %q", name, r, err, tc.says)
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": tc.args}})
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		rec := httptest.NewRecorder()
		m.Handler().ServeHTTP(rec, req)
		if !strings.Contains(rec.Body.String(), tc.says) {
			t.Errorf("%s through the HTTP handler = %d %s; want %q", name, rec.Code, rec.Body, tc.says)
		}
	}
}
