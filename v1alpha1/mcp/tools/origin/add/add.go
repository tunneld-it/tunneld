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
	mcperrors "github.com/tunnel-pizza/tunneld/v1alpha1/mcp/errors"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/errors/refusals"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/tool"
	"github.com/tunnel-pizza/tunneld/v1alpha1/mcp/types"
	"github.com/tunnel-pizza/tunneld/v1alpha1/origins"
	"github.com/tunnel-pizza/tunneld/v1alpha1/util/netutil"
	"golang.org/x/net/http/httpguts"
)

// Tool is origin_add.
var Tool = tool.New(sdk.Tool{
	Name:        "origin_add",
	Title:       "Add an origin",
	Description: "Adds a web address on a public name or address to this machine's tunnel, such as https://api.example.com, reached at the next ?n with any headers given set on every request it is forwarded. Answers the machine and the new origin's session, its address among them. Programs, containers and addresses on this machine or its network are the ones tunneld is started with. Only the client that started this tunneld, over stdio, the tunnel's secret, or a sign-in holding tunnel:origins, may call it.",
	// Adds an address the tunnel forwards to, and takes nothing away.
	Annotations: &sdk.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(true)},
}, run())

// In is what origin_add takes.
type In struct {
	Origin  string            `json:"origin" jsonschema:"the origin, a web address on a public name or address: https://api.example.com, or api.example.com:8443 for http"`
	Headers map[string]string `json:"headers,omitempty" jsonschema:"request headers set on every request the tunnel forwards to this origin, by name, replacing any a visitor sent under that name: credentials a remote endpoint wants, such as X-Api-Key. Optional"`
}

// managed is what tunneld sets on a forwarded request itself, or what manages
// the connection rather than saying anything to the origin.
var managed = map[string]bool{
	"Host": true, "Connection": true, "Keep-Alive": true, "Proxy-Connection": true,
	"Transfer-Encoding": true, "Upgrade": true, "Te": true, "Trailer": true, "Content-Length": true,
}

// run adds a web address on a public name to the run's origins.
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
		if origin.Scheme != v1.HttpScheme {
			return refuse(refusals.NotYetServed(origin))
		}
		if netutil.Local(origin.URI.Hostname()) {
			return refuse(refusals.NotRemote(origin))
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
		added := c.Origins.Append(origin)
		machine := c.Machine()
		c.Logged(ctx, "origin_add", "ok", start)
		return nil, types.Origin{
			Machine: machine,
			Session: v1types.Session{Origin: added, Address: tool.Address(machine.Tunnel, added.Index, c.Origins.Len())},
		}, nil
	}
}
