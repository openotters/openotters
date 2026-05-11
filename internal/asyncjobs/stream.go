package asyncjobs

import (
	"context"
	"sync"
	"time"
)

// streamFlushInterval is how often the debounced sink wakes up to
// push accumulated bytes into the store. 200 ms is short enough that
// UI observers polling at 1 s feel "live", long enough to coalesce
// chatty BINs (yaegi printing line-by-line, npm install's progress
// spinner) into a small number of SQL hits per second instead of
// one-write-per-byte. Constant rather than configurable for now —
// every async-job has the same UX expectation.
const streamFlushInterval = 200 * time.Millisecond

// appendFunc is the slice of *Store the sink needs: append a chunk
// to one column on one row. The pool wires the row id once and
// hands the writer a closure that already knows which column it's
// targeting (stdout vs stderr), so the sink itself is column-agnostic.
type appendFunc func(ctx context.Context, chunk []byte) error

// streamSink is an io.Writer that buffers chunks in memory and
// flushes them through `append` on a fixed cadence. It exists so the
// docker / system executors can call Write at whatever rate the BIN
// produces output without each Write hitting SQLite — the flusher
// goroutine batches everything into one UPDATE per
// streamFlushInterval.
//
// Lifetime: caller New()s one sink per stdout/stderr per job,
// passes it through executor.WithExecStreamSinks for the duration
// of Exec, and Close()s when Exec returns. Close flushes the tail
// and stops the goroutine — required to avoid leaking goroutines
// per finished job.
//
// Concurrent Writes are safe (the executor may call from multiple
// goroutines, e.g. the docker logs demuxer); Close is NOT safe to
// call concurrently with Writes. Callers serialise that ordering
// via Exec's return.
type streamSink struct {
	appendFn appendFunc
	logger   func(error)

	mu  sync.Mutex
	buf []byte

	stop     chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once
}

// newStreamSink starts the flusher goroutine and returns a writer
// ready to receive bytes. `append` is the column-targeting closure
// (typically `func(ctx, chunk) error { return store.AppendStdout(ctx, jobID, chunk) }`).
// `onErr` is invoked on each flush failure — appends are best-effort,
// so the writer never returns these errors to its Write caller (that
// would surprise the executor); the logger captures them instead.
func newStreamSink(fn appendFunc, onErr func(error)) *streamSink {
	s := &streamSink{
		appendFn: fn,
		logger:   onErr,
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}

	go s.run()

	return s
}

// Write satisfies io.Writer. Always returns len(p), nil — backpressure
// from SQL would distort the executor's read loop (slowing down
// docker log demux is worse than dropping a periodic flush). The
// real write happens in run().
func (s *streamSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.buf = append(s.buf, p...)
	s.mu.Unlock()

	return len(p), nil
}

// Close flushes any buffered tail and stops the goroutine. Idempotent.
func (s *streamSink) Close() error {
	s.stopOnce.Do(func() {
		close(s.stop)
		<-s.stopped
		s.flushOnce(context.Background())
	})

	return nil
}

func (s *streamSink) run() {
	defer close(s.stopped)

	ticker := time.NewTicker(streamFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.flushOnce(context.Background())
		case <-s.stop:
			return
		}
	}
}

// flushOnce drains the buffer atomically and pushes the snapshot
// through `append`. Acquires the mutex only long enough to swap
// buffers so concurrent Writes don't block on SQL latency.
func (s *streamSink) flushOnce(ctx context.Context) {
	s.mu.Lock()
	if len(s.buf) == 0 {
		s.mu.Unlock()
		return
	}

	chunk := s.buf
	s.buf = nil
	s.mu.Unlock()

	if err := s.appendFn(ctx, chunk); err != nil && s.logger != nil {
		s.logger(err)
	}
}
