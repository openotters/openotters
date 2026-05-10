package helper

import (
	"bytes"
	"sync"
	"testing"
)

// prefixWriter is an io.Writer that pipes the daemon subprocess's
// stdout/stderr into the test's t.Log with a "[ottersd:executor:out]"
// prefix per line. Keeps daemon noise visible when -v is on but
// silenced when it's off.
type prefixWriter struct {
	t      *testing.T
	prefix string

	mu  sync.Mutex
	buf bytes.Buffer
}

func newPrefixWriter(t *testing.T, prefix string) *prefixWriter {
	return &prefixWriter{t: t, prefix: prefix}
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf.Write(p)

	for {
		line, err := w.buf.ReadBytes('\n')
		if err != nil {
			// Partial line — put it back for the next Write.
			w.buf.Reset()
			w.buf.Write(line)
			break
		}
		w.t.Logf("%s%s", w.prefix, string(bytes.TrimRight(line, "\n")))
	}

	return len(p), nil
}
