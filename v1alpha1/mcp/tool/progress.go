package tool

import (
	"context"
	"fmt"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// How long an event waits for quiet before it is sent, and how long a call
// goes with nothing sent before a heartbeat.
var (
	settle    = time.Second
	heartbeat = 5 * time.Second
)

// progress is one call's notifications/progress, for a client that asked
// with a progress token: what a tool says it is doing, once it has said
// nothing newer for settle, and after heartbeat with nothing sent, the same
// again with how long the call has run. progress counts up from 1 with no
// total; _meta names the tool. A send that fails is dropped, never the call.
//
// Every message is one a tool wrote, never a program's output or a call's
// arguments.
type progress struct {
	session *sdk.ServerSession
	ctx     context.Context
	token   any
	tool    string
	start   time.Time

	mu     sync.Mutex
	n      float64
	doing  string
	event  *time.Timer
	beat   *time.Timer
	closed bool
}

// progressKey is where a call's progress rides its context.
type progressKey struct{}

// startProgress is the progress of the call req makes to tool, and ctx
// carrying it; nil, and ctx as it is, when the client asked for none.
func startProgress(ctx context.Context, req *sdk.CallToolRequest, tool string) (*progress, context.Context) {
	if req == nil || req.Session == nil || req.Params == nil || req.Params.GetProgressToken() == nil {
		return nil, ctx
	}
	p := &progress{session: req.Session, ctx: ctx, token: req.Params.GetProgressToken(), tool: tool, start: time.Now(), doing: "Working"}
	p.beat = time.AfterFunc(heartbeat, p.heartbeat)
	return p, context.WithValue(ctx, progressKey{}, p)
}

// Progress says what the call ctx carries is doing now, for a client that
// asked to be told; nothing otherwise.
func Progress(ctx context.Context, doing string) {
	p, _ := ctx.Value(progressKey{}).(*progress)
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.doing = doing
	if p.event != nil {
		p.event.Stop()
	}
	p.event = time.AfterFunc(settle, func() { p.send(doing) })
}

// heartbeat sends what the call is doing with how long it has run.
func (p *progress) heartbeat() {
	p.mu.Lock()
	doing := p.doing
	p.mu.Unlock()
	p.send(fmt.Sprintf("%s, %ds", doing, int(time.Since(p.start).Round(time.Second).Seconds())))
}

// send sends message, one at a time and none once the call has answered,
// and puts the next heartbeat off.
func (p *progress) send(message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.n++
	_ = p.session.NotifyProgress(p.ctx, &sdk.ProgressNotificationParams{
		ProgressToken: p.token,
		Progress:      p.n,
		Message:       message,
		Meta:          sdk.Meta{"tunneld.it/tool": p.tool},
	})
	p.beat.Reset(heartbeat)
}

// close ends the call's progress before its answer goes out: a send under
// way finishes first, and nothing is sent after.
func (p *progress) close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.beat.Stop()
	if p.event != nil {
		p.event.Stop()
	}
}
