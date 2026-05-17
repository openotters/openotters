// Package observability hosts the daemon's always-on RPC observer:
// a bounded ring buffer of recent RPC calls + a subscriber registry
// fed by an http.Handler middleware (middleware.go). The Connect
// streaming RPC StreamRPCCalls (handled in internal/runtime_handler.go)
// tails the buffer for the operator UI.
//
// Two invariants the rest of the daemon relies on:
//
//  1. Every Connect call hitting the daemon is recorded — including
//     auth-failed and panic'd ones — without any per-handler wiring.
//     A new RPC added to any service shows up in the monitor on the
//     next daemon start. The middleware runs at the HTTP layer
//     OUTSIDE per-service Connect interceptors, so future services
//     are automatically captured.
//  2. The producer never blocks. Slow subscribers get their oldest
//     events dropped from their per-subscriber channel; the recorder
//     itself never stalls a request.
package observability

import (
	"sync"
	"time"
)

// RPCCall is one row in the ring buffer + one delivery on the
// subscriber channel. Captured at the HTTP layer, so fields are
// derived from the request path / response status, not from typed
// Connect message accessors.
//
// Caller is "operator" / "agent:<agent_ref>" / "anonymous":
// filled in by the JWTInterceptor when auth succeeds, left as
// "anonymous" otherwise. The middleware reads it via the shared
// callInfo struct injected into the request context.
type RPCCall struct {
	Timestamp  time.Time
	Service    string // e.g. "Runtime" — parsed from the URL path
	Procedure  string // e.g. "SaveAgentNote"
	Caller     string
	Status     string // "ok" on 200; otherwise the lowercase Connect code or "http_<status>"
	Duration   time.Duration
	BytesIn    int64
	BytesOut   int64
	ErrMessage string // populated when Status != "ok"
	StreamType string // "unary" / "server-stream" — derived from response Content-Type
}

const (
	// defaultBufferSize bounds the ring. 2000 calls = a few minutes
	// of busy daemon traffic; pages can request replay_recent up to
	// this cap.
	defaultBufferSize = 2000
	// subscriberChanSize bounds each subscriber's per-tab channel.
	// Slow consumers DROP oldest events rather than backpressure the
	// producer.
	subscriberChanSize = 256
)

// Recorder is safe for concurrent access from the HTTP request
// goroutines (producer) and from any number of subscriber
// goroutines (consumers).
type Recorder struct {
	mu          sync.Mutex
	buf         []RPCCall // ring; len(buf) <= cap
	cap         int       // ring capacity
	subscribers map[int]chan RPCCall
	nextSubID   int
}

// NewRecorder returns a fresh recorder with the default buffer
// capacity. capacity overrides the default when > 0; useful for
// tests that want a tiny ring to exercise the drop-oldest path.
func NewRecorder(capacity int) *Recorder {
	if capacity <= 0 {
		capacity = defaultBufferSize
	}
	return &Recorder{
		cap:         capacity,
		buf:         make([]RPCCall, 0, capacity),
		subscribers: make(map[int]chan RPCCall),
	}
}

// Push records one call into the ring and fans it out to every
// subscriber. Channels that are full drop the call for that
// subscriber only — the producer is never blocked. This is the
// load-bearing rule: a slow browser tab MUST NOT slow the
// daemon's request path.
func (r *Recorder) Push(call RPCCall) {
	r.mu.Lock()
	if len(r.buf) >= r.cap {
		// Shift one out; oldest-first eviction. copy is allocation-
		// free for the common case (no growth).
		copy(r.buf, r.buf[1:])
		r.buf[len(r.buf)-1] = call
	} else {
		r.buf = append(r.buf, call)
	}
	subs := make([]chan RPCCall, 0, len(r.subscribers))
	for _, ch := range r.subscribers {
		subs = append(subs, ch)
	}
	r.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- call:
		default:
			// Drop on full. The subscriber's UI sees a gap; the
			// producer keeps running. Replay_recent on resubscribe
			// can fill it back in if the operator cares.
		}
	}
}

// Subscribe returns a channel that receives every future Push
// (subject to the per-subscriber drop policy) plus a cancel
// function the caller MUST call on stream close. The channel is
// closed by cancel; never close it from the producer side.
//
// replayRecent: send up to N most-recent buffered calls before
// going live. Clamped to buffer capacity. Replayed events are
// delivered synchronously before the channel becomes live — the
// subscriber sees them in oldest-first order.
func (r *Recorder) Subscribe(replayRecent int) (<-chan RPCCall, func()) {
	r.mu.Lock()
	id := r.nextSubID
	r.nextSubID++
	ch := make(chan RPCCall, subscriberChanSize)
	r.subscribers[id] = ch

	if replayRecent > 0 {
		// Slice copy under the lock so we don't race with a
		// concurrent Push appending to the same buf.
		start := len(r.buf) - replayRecent
		if start < 0 {
			start = 0
		}
		replay := make([]RPCCall, len(r.buf)-start)
		copy(replay, r.buf[start:])
		r.mu.Unlock()
		// Deliver outside the lock — same drop policy as live
		// events. Replay is best-effort; a frozen subscriber loses
		// some history rather than holding everyone else up.
		for _, c := range replay {
			select {
			case ch <- c:
			default:
			}
		}
	} else {
		r.mu.Unlock()
	}

	cancel := func() {
		r.mu.Lock()
		if existing, ok := r.subscribers[id]; ok && existing == ch {
			delete(r.subscribers, id)
			close(ch)
		}
		r.mu.Unlock()
	}
	return ch, cancel
}

// Snapshot returns a copy of the current ring (oldest-first). Used
// by tests; production callers go through Subscribe.
func (r *Recorder) Snapshot() []RPCCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RPCCall, len(r.buf))
	copy(out, r.buf)
	return out
}
