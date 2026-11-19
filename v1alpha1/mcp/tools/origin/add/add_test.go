package add

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	v1 "github.com/tunnel-pizza/tunneld/v1"
	"github.com/tunnel-pizza/tunneld/v1alpha1/attach"
	"github.com/tunnel-pizza/tunneld/v1alpha1/auth"
	"github.com/tunnel-pizza/tunneld/v1alpha1/cache"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/tool"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/tool/tooltest"
	"github.com/tunnel-pizza/tunneld/v1alpha1/origins"
)

// TestOriginAddListing pins how origin_add is listed: it changes the run,
// adds and takes nothing away, reaches out past the machine, and takes an
// optional headers object of strings.
func TestOriginAddListing(t *testing.T) {
	listed := tooltest.Listed(t, Tool)
	if a := listed.Annotations; a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint || a.OpenWorldHint == nil || !*a.OpenWorldHint {
		t.Errorf("annotations = %+v, want a change, destructive: false, open world", a)
	}
	if strings.Contains(listed.Description, "Not implemented") {
		t.Errorf("description %q still says it is not implemented", listed.Description)
	}
	schema, _ := json.Marshal(listed.InputSchema)
	var in struct {
		Properties map[string]struct {
			Type                 any `json:"type"`
			AdditionalProperties struct {
				Type any `json:"type"`
			} `json:"additionalProperties"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &in); err != nil {
		t.Fatalf("input schema %s: %v", schema, err)
	}
	if h, ok := in.Properties["headers"]; !ok || h.Type != "object" || h.AdditionalProperties.Type != "string" {
		t.Errorf("input schema %s, want headers an object of strings", schema)
	}
	if slices.Contains(in.Required, "headers") {
		t.Errorf("input schema requires headers, want it optional: %s", schema)
	}
}

// TestOriginAddGate pins who may call it over the tunnel: a token without
// tunnel:origins is refused, saying why; the secret and a token with the
// scope add the origin.
func TestOriginAddGate(t *testing.T) {
	c := tool.Context{Origins: tooltest.Origins(t, "exec:///bin/sh"), Log: slog.New(slog.DiscardHandler)}
	for name, tc := range map[string]struct {
		access *auth.Access
		says   string
	}{
		"the tunnel's secret":         {nil, ""},
		"a token without the scope":   {&auth.Access{Sub: "github:1", Scopes: []string{"mcp", "tunnel:exec"}}, "origin_add needs the tunnel:origins scope"},
		"a token with tunnel:origins": {&auth.Access{Sub: "github:1", Scopes: []string{"mcp", "tunnel:origins"}}, ""},
	} {
		status, body := tooltest.Call(t, Tool, c, tc.access, `{"origin":":3000"}`)
		if tc.says == "" {
			if status != 200 || strings.Contains(body, `"isError":true`) || !strings.Contains(body, `"ref":"localhost:3000"`) {
				t.Errorf("origin_add over the tunnel, %s = %d %s, want localhost:3000 added", name, status, body)
			}
			continue
		}
		if status != 200 || !strings.Contains(body, tc.says) || !strings.Contains(body, `"isError":true`) {
			t.Errorf("origin_add over the tunnel, %s = %d %s, want a tool error saying %q", name, status, body, tc.says)
		}
	}
}

// TestOriginAdd pins an add: a web address joins the run
// at the next index, marked added, with the headers given, canonical, and
// answers its session and address, the headers never in the answer; the
// run's file then lists it, for -l.
func TestOriginAdd(t *testing.T) {
	list := origins.New(origins.WithOrigins(tooltest.Origins(t, "http://localhost:3000").All()...))
	tracked := cache.New(cache.WithDir(t.TempDir()), cache.WithOrigins(list), cache.WithSpec(`{"spec":{}}`), cache.WithTracking(map[string]string{"LIBTUNNEL_HOSTNAME": "0tabc.tunneld.domain"}))
	c := tool.Context{Origins: list, Cache: tracked, Log: slog.New(slog.DiscardHandler)}

	r, out, err := run()(c, t.Context(), &sdk.CallToolRequest{}, In{
		Origin:  "https://04d0ee3e.lambda-microvm.us-east-1.on.aws",
		Headers: map[string]string{"x-aws-proxy-auth": "the token", "X-Aws-Proxy-Port": "8000"},
	})
	if err != nil || r != nil {
		t.Fatalf("origin_add = %+v, %v; want an answer", r, err)
	}
	if s := out.Session; s.Index != 1 || s.Scheme != "http" || s.Ref != "04d0ee3e.lambda-microvm.us-east-1.on.aws" || s.Address != "https://0tabc.tunneld.domain/?1" {
		t.Errorf("session = %+v, want index 1, http, its host, at ?1", s)
	}
	if out.Machine.Tunnel != "0tabc.tunneld.domain" {
		t.Errorf("machine = %+v, want the tunnel's", out.Machine)
	}
	if list.Len() != 2 {
		t.Fatalf("the run has %d origins, want 2", list.Len())
	}
	got := list.At(1)
	if !got.Added || got.Headers.Get("X-Aws-Proxy-Auth") != "the token" || got.Headers.Get("X-Aws-Proxy-Port") != "8000" || len(got.Headers) != 2 {
		t.Errorf("origin 1 = %+v, want added, with both headers, canonical", got)
	}
	if answer, _ := json.Marshal(out); strings.Contains(string(answer), "the token") {
		t.Errorf("answer %s carries a header's value", answer)
	}
	if file, _ := os.ReadFile(tracked.Path(list)); !strings.Contains(string(file), "TUNNELD_ADDED_ORIGINS='https://04d0ee3e.lambda-microvm.us-east-1.on.aws'") || strings.Contains(string(file), "the token") {
		t.Errorf("the run's file =\n%s\nwant the added origin, and no header's value", file)
	}
	for i, word := range []string{":4000", "http://web:3000", "http://10.0.0.5:8080"} {
		_, out, _ := run()(c, t.Context(), &sdk.CallToolRequest{}, In{Origin: word})
		if out.Session.Index != 2+i || !list.At(2+i).Added {
			t.Errorf("origin_add(%q) = %+v, want it added at %d like any other", word, out.Session, 2+i)
		}
	}
}

// TestOriginAddRefuses pins what it will not add, each saying why, with the
// run left as it was: something that is no origin, a program on a run with
// nothing to serve it, headers for a program, and a header a request cannot
// carry or that tunneld sets.
func TestOriginAddRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		in   In
		says string
	}{
		"nothing":              {In{Origin: ""}, `"" is not an origin`},
		"a program, no server": {In{Origin: "exec:///bin/sh"}, "This run's origins are fixed"},
		"headers on a program": {In{Origin: "exec:///bin/sh", Headers: map[string]string{"X-Api": "k"}}, `header "X-Api": only a web address is sent headers`},
		"Host":                 {In{Origin: "https://api.example.com", Headers: map[string]string{"host": "evil.example"}}, `header "host": tunneld sets it`},
		"not a header name":    {In{Origin: "https://api.example.com", Headers: map[string]string{"x api": "k"}}, `header "x api": not a header name`},
		"a line break value":   {In{Origin: "https://api.example.com", Headers: map[string]string{"X-Api": "k\r\nX-Evil: 1"}}, `header "X-Api": its value is not one a header can carry`},
	} {
		t.Run(name, func(t *testing.T) {
			list := origins.New()
			c := tool.Context{Origins: list, Log: slog.New(slog.DiscardHandler)}
			r, _, err := run()(c, t.Context(), &sdk.CallToolRequest{}, tc.in)
			if err != nil || r == nil || !r.IsError || !strings.Contains(r.Content[0].(*sdk.TextContent).Text, tc.says) {
				t.Errorf("origin_add(%+v) = %+v, %v; want a tool error saying %q", tc.in, r, err, tc.says)
			}
			if list.Len() != 0 {
				t.Errorf("the run has %d origins after a refusal, want none", list.Len())
			}
		})
	}
	r, _, _ := run()(tool.Context{Log: slog.New(slog.DiscardHandler)}, t.Context(), &sdk.CallToolRequest{}, In{Origin: "https://api.example.com"})
	if r == nil || !r.IsError || !strings.Contains(r.Content[0].(*sdk.TextContent).Text, "This run's origins are fixed") {
		t.Errorf("origin_add on a run with no origins to change = %+v, want it refused", r)
	}
}

// served stands in for the run's servers: Add appends what it is given as
// served at a loopback address, or fails with err; Announce keeps what it
// was told.
type served struct {
	attach.Attached
	list      *origins.OriginsImpl
	err       error
	announced []string
}

func (s *served) Add(o v1.Origin) (v1.Origin, error) {
	if s.err != nil {
		return v1.Origin{}, s.err
	}
	return s.list.AppendServed(o, &url.URL{Scheme: "http", Host: "127.0.0.1:5000"}), nil
}

func (s *served) Announce(public []string) { s.announced = public }

// TestOriginAddServesForStdioAndTheSecret pins who may start a program or
// attach a container: the stdio client and the tunnel's secret; a sign-in,
// whatever its scopes, is refused and nothing is served, while it may still
// add a web address.
func TestOriginAddServesForStdioAndTheSecret(t *testing.T) {
	signIn := &auth.Access{Sub: "github:1", Scopes: []string{"mcp", "tunnel:origins", "tunnel:exec", "tunnel:attach"}}
	for name, tc := range map[string]struct {
		access *auth.Access
		origin string
		says   string
	}{
		"the secret, a program":    {nil, "exec:///bin/bash", `"index":1`},
		"a sign-in, a program":     {signIn, "exec:///bin/bash", "This sign-in can't add exec:///bin/bash: it is exec"},
		"a sign-in, a container":   {signIn, "attach://dockerd/web", "This sign-in can't add attach://dockerd/web: it is attach"},
		"a sign-in, a web address": {signIn, "https://api.example.com", `"index":1`},
	} {
		t.Run(name, func(t *testing.T) {
			list := origins.New(origins.WithOrigins(tooltest.Origins(t, "http://localhost:3000").All()...))
			c := tool.Context{Origins: list, Attached: &served{list: list}, Log: slog.New(slog.DiscardHandler)}
			args, _ := json.Marshal(map[string]string{"origin": tc.origin})
			status, body := tooltest.Call(t, Tool, c, tc.access, string(args))
			if status != 200 || !strings.Contains(body, tc.says) {
				t.Errorf("origin_add(%s) = %d %s, want %q", tc.origin, status, body, tc.says)
			}
			if refused := strings.Contains(tc.says, "can't"); refused && list.Len() != 1 {
				t.Errorf("a refused add left %d origins, want 1", list.Len())
			}
		})
	}
	r, _, err := run()(tool.Context{Origins: origins.New(), Attached: &served{list: origins.New()}, Log: slog.New(slog.DiscardHandler)}, t.Context(), &sdk.CallToolRequest{}, In{Origin: "exec:///bin/bash"})
	if err != nil || r != nil {
		t.Errorf("origin_add of a program over stdio = %+v, %v; want it served", r, err)
	}
}

// TestOriginAddServes pins a program and a container: each handed to the
// run's servers, which serve it before it is added, and every served origin
// told its address again, the first one's bare address now ?0. One that
// cannot be served adds nothing, saying why.
func TestOriginAddServes(t *testing.T) {
	list := origins.New(origins.WithOrigins(tooltest.Origins(t, "exec:///bin/sh").All()...))
	tracked := cache.New(cache.WithDir(""), cache.WithTracking(map[string]string{"LIBTUNNEL_HOSTNAME": "0tabc.tunneld.domain"}))
	servers := &served{list: list}
	c := tool.Context{Origins: list, Attached: servers, Cache: tracked, Log: slog.New(slog.DiscardHandler)}

	for i, word := range []string{"exec:///bin/zsh", "attach://dockerd/web"} {
		r, out, err := run()(c, t.Context(), &sdk.CallToolRequest{}, In{Origin: word})
		if err != nil || r != nil || out.Session.Index != 1+i || out.Session.Address != fmt.Sprintf("https://0tabc.tunneld.domain/?%d", 1+i) {
			t.Fatalf("origin_add(%q) = %+v, %+v, %v; want it served at index %d", word, r, out.Session, err, 1+i)
		}
	}
	want := []string{"https://0tabc.tunneld.domain/?0", "https://0tabc.tunneld.domain/?1", "https://0tabc.tunneld.domain/?2"}
	if !slices.Equal(servers.announced, want) {
		t.Errorf("announced %q, want %q", servers.announced, want)
	}

	servers.err = errors.New("no such container")
	r, _, _ := run()(c, t.Context(), &sdk.CallToolRequest{}, In{Origin: "attach://dockerd/gone"})
	if r == nil || !r.IsError || r.Content[0].(*sdk.TextContent).Text != "Could not serve attach://dockerd/gone: no such container" || list.Len() != 3 {
		t.Errorf("origin_add of a container that is not there = %+v, %d origins; want it refused, nothing added", r, list.Len())
	}
}
