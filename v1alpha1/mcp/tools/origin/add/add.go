// Package add is the origin_add tool: a new origin for the running tunnel,
// read the way the command line reads one.
package add

import (
	"context"
	"errors"
	"net/http"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	v1 "github.com/tunnel-pizza/tunneld/v1"
	v1types "github.com/tunnel-pizza/tunneld/v1/types"
	"github.com/tunnel-pizza/tunneld/v1alpha1/auth"
	mcperrors "github.com/tunnel-pizza/tunneld/v1alpha1/mcp/errors"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/errors/refusals"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/tool"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/types"
	"github.com/tunnel-pizza/tunneld/v1alpha1/origins"
	"golang.org/x/net/http/httpguts"
)

// Tool is origin_add.
var Tool = tool.New(sdk.Tool{
	Name:        "origin_add",
	Title:       "Add an origin",
	Description: "Adds an origin to this machine's tunnel, as tunneld's command line takes one: a web address (:3000, https://api.example.com), a program (sh, exec:///bin/bash) or a container (attach://dockerd/web), reached at the next ?n. A program starts at once; a web address is sent any headers given on every request it is forwarded. Answers the machine and the new origin's session, its address among them. Only the client that started this tunneld, over stdio, the tunnel's secret, or a sign-in holding tunnel:origins, may call it, and a sign-in adds web addresses only.",
	// Adds an origin, starting a program or attaching a container, and takes
	// nothing away.
	Annotations: &sdk.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(true)},
}, run())

// In is what origin_add takes.
type In struct {
	Origin  string            `json:"origin" jsonschema:"the origin, as tunneld's command line takes one: a port (:3000), a program on this machine (sh), or a URL (http://localhost:3000, https://api.example.com, exec:///bin/bash, attach://dockerd/web)"`
	Headers map[string]string `json:"headers,omitempty" jsonschema:"for a web address: request headers set on every request the tunnel forwards to it, by name, replacing any a visitor sent under that name: credentials a remote endpoint wants, such as X-Api-Key. Optional"`
}

// managed is what tunneld sets on a forwarded request itself, or what manages
// the connection rather than saying anything to the origin.
var managed = map[string]bool{
	"Host": true, "Connection": true, "Keep-Alive": true, "Proxy-Connection": true,
	"Transfer-Encoding": true, "Upgrade": true, "Te": true, "Trailer": true, "Content-Length": true,
}

// run adds an origin to the run's origins.
func run() tool.RunFunc[In, types.Origin] {
	return func(c tool.Context, ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, types.Origin, error) {
		start := time.Now()
		if err := c.ChangesOrigins(ctx, req, "origin_add", start); err != nil {
			return mcperrors.ErrOriginAdd(err)
		}
		refuse := func(err error) (*sdk.CallToolResult, types.Origin, error) {
			c.Logged(ctx, "origin_add", "refused", start)
			return mcperrors.ErrOriginAdd(err)
		}
		if c.Origins == nil {
			return refuse(refusals.Fixed("origin_add"))
		}
		parsed := origins.Parse(c.Log, []string{in.Origin}).All()
		if len(parsed) != 1 {
			return refuse(refusals.NotAnOrigin(in.Origin))
		}
		origin := parsed[0]
		// A program or a container is a terminal on this machine: a sign-in
		// may add a web address, and only the client that started this
		// tunneld, over stdio, or the tunnel's secret may start one.
		if origin.Scheme != v1.HttpScheme && !tool.Stdio(req) {
			if _, byToken := auth.AccessOf(ctx); byToken {
				return refuse(refusals.ServesBySignIn(origin))
			}
		}
		// Headers ride requests the router forwards, which only a web
		// address is sent.
		if origin.Scheme != v1.HttpScheme {
			for name := range in.Headers {
				return refuse(refusals.BadHeader(name, errors.New("only a web address is sent headers")))
			}
		}
		if len(in.Headers) > 0 {
			origin.Headers = http.Header{}
			for name, value := range in.Headers {
				switch {
				case !httpguts.ValidHeaderFieldName(name):
					return refuse(refusals.BadHeader(name, errors.New("not a header name")))
				case managed[http.CanonicalHeaderKey(name)]:
					return refuse(refusals.BadHeader(name, errors.New("tunneld sets it")))
				case !httpguts.ValidHeaderFieldValue(value):
					return refuse(refusals.BadHeader(name, errors.New("its value is not one a header can carry")))
				}
				origin.Headers.Set(name, value)
			}
		}
		// A web address is dialed where it is. A program or a container is
		// served on loopback first and added with that address, so the
		// router never dials it before it answers.
		var added v1.Origin
		if origin.Scheme == v1.HttpScheme {
			added = c.Origins.Append(origin)
		} else {
			if c.Attached == nil {
				return refuse(refusals.Fixed("origin_add"))
			}
			if origin.Scheme == v1.ExecScheme {
				tool.Progress(ctx, "Starting the program")
			} else {
				tool.Progress(ctx, "Attaching the container")
			}
			var err error
			if added, err = c.Attached.Add(origin); err != nil {
				return refuse(refusals.Unserved(origin, err))
			}
		}
		// The run's file lists what it serves, for -l.
		if c.Cache != nil {
			c.Cache.Save()
		}
		machine := c.Machine()
		// Every served origin is told its address again: with one origin
		// more, the first may have gone from the bare address to ?0.
		if c.Attached != nil && machine.Tunnel != "" {
			public := make([]string, c.Origins.Len())
			for i := range public {
				public[i] = tool.Address(machine.Tunnel, i, len(public))
			}
			c.Attached.Announce(public)
		}
		c.Logged(ctx, "origin_add", "ok", start)
		return nil, types.Origin{
			Machine: machine,
			Session: v1types.Session{Origin: added, Address: tool.Address(machine.Tunnel, added.Index, c.Origins.Len())},
		}, nil
	}
}
