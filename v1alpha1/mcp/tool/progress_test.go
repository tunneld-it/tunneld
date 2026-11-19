package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestProgress pins a call's progress, over the stateless streamable HTTP
// the tunnel serves: with a token, what the tool says once it has settled,
// then heartbeats saying it again with how long the call has run, progress
// counting up from 1, the tool named in _meta, and nothing after the
// answer; nothing for something said and done within settle, and nothing
// at all without a token.
func TestProgress(t *testing.T) {
	settle, heartbeat = 20*time.Millisecond, 60*time.Millisecond
	t.Cleanup(func() { settle, heartbeat = time.Second, 5*time.Second })

	type In struct {
		Ms int `json:"ms"`
	}
	slow := New(sdk.Tool{Name: "slow"}, func(_ Context, ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, struct{}, error) {
		Progress(ctx, "Waiting for the command to end")
		time.Sleep(time.Duration(in.Ms) * time.Millisecond)
		return nil, struct{}{}, nil
	})
	server := sdk.NewServer(&sdk.Implementation{Name: "t"}, nil)
	slow.Add(server, Context{})
	h := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(h.Close)

	var mu sync.Mutex
	var got []*sdk.ProgressNotificationParams
	client := sdk.NewClient(&sdk.Implementation{Name: "c"}, &sdk.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *sdk.ProgressNotificationClientRequest) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, r.Params)
		},
	})
	cs, err := client.Connect(t.Context(), &sdk.StreamableClientTransport{Endpoint: h.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	call := func(ms int, token any) []*sdk.ProgressNotificationParams {
		t.Helper()
		mu.Lock()
		got = nil
		mu.Unlock()
		params := &sdk.CallToolParams{Name: "slow", Arguments: map[string]any{"ms": ms}}
		if token != nil {
			params.SetProgressToken(token)
		}
		if _, err := cs.CallTool(t.Context(), params); err != nil {
			t.Fatalf("slow: %v", err)
		}
		time.Sleep(3 * heartbeat)
		mu.Lock()
		defer mu.Unlock()
		return got
	}

	sent := call(200, "t1")
	if len(sent) < 3 || sent[0].Message != "Waiting for the command to end" || !strings.HasPrefix(sent[1].Message, "Waiting for the command to end, ") {
		t.Fatalf("progress over a 200ms call = %s, want the event, then heartbeats repeating it", messages(sent))
	}
	for i, p := range sent {
		if p.Progress != float64(i+1) || p.Total != 0 || p.ProgressToken != "t1" || p.Meta["tunneld.it/tool"] != "slow" {
			t.Errorf("notification %d = %+v, want progress %d, no total, the call's token, the tool in _meta", i, p, i+1)
		}
	}
	if n := len(sent); n > 200/60+2 {
		t.Errorf("%d notifications over 200ms, want none after the answer: %s", n, messages(sent))
	}

	if sent := call(0, "t2"); len(sent) != 0 {
		t.Errorf("progress for a call done within settle = %s, want none", messages(sent))
	}
	if sent := call(200, nil); len(sent) != 0 {
		t.Errorf("progress without a token = %s, want none", messages(sent))
	}
}

func messages(ps []*sdk.ProgressNotificationParams) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Message)
	}
	return "[" + strings.Join(out, " | ") + "]"
}
