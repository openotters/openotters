package observability

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// callInfoKey is the typed context key the JWT interceptor and the
// recorder middleware share. Unexported so no other package can
// collide; the helper accessors below are the only API.
type callInfoCtxKey int

const callInfoKey callInfoCtxKey = 0

// CallInfo is the mutable struct the middleware injects into the
// request context BEFORE the JWT interceptor runs. JWT fills in
// Caller (and AgentRef on agent tokens); the middleware reads it
// after the handler returns. Mutable-pointer-in-context is the
// idiomatic way to pass a "fill this in" struct down the stack
// without the recorder having to live inside JWT.
type CallInfo struct {
	Caller   string // "operator" / "agent:<ref>" / "anonymous"
	AgentRef string // non-empty when Caller starts with "agent:"
}

// CallInfoFromContext returns the per-request CallInfo or nil when
// the middleware didn't run (in tests, mostly).
func CallInfoFromContext(ctx context.Context) *CallInfo {
	v, _ := ctx.Value(callInfoKey).(*CallInfo)
	return v
}

// Middleware wraps an http.Handler so every request gets recorded
// after it returns. Mount this OUTSIDE the JWT interceptor — the
// recorder needs to see auth-failed calls too, and JWT will write
// Caller into the CallInfo struct the middleware injected.
type Middleware struct {
	rec *Recorder
}

func NewMiddleware(rec *Recorder) *Middleware {
	return &Middleware{rec: rec}
}

// Wrap returns the http.Handler the serve mux mounts.
//
// Records every request as ONE RPCCall. The procedure path is
// pulled from the URL (Connect's wire layout uses
// `/<package>.<service>/<rpc>`); failures show up with a non-"ok"
// Status. Bodies are never inspected — only sizes via wrappers.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip the StreamRPCCalls RPC itself — recording every
		// subscription read on top of the underlying events would
		// produce a feedback loop on busy clients (the subscriber's
		// own keep-alive frames would re-enter the recorder).
		if isStreamRPCCallsPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		info := &CallInfo{Caller: "anonymous"}
		ctx := context.WithValue(r.Context(), callInfoKey, info)

		start := time.Now()
		cr := &countingReadCloser{ReadCloser: r.Body}
		r.Body = cr
		cw := &countingResponseWriter{ResponseWriter: w}

		next.ServeHTTP(cw, r.WithContext(ctx))

		service, procedure := parseProcedurePath(r.URL.Path)
		status, errMsg := classifyStatus(cw.status)

		m.rec.Push(RPCCall{
			Timestamp:  start,
			Service:    service,
			Procedure:  procedure,
			Caller:     info.Caller,
			Status:     status,
			Duration:   time.Since(start),
			BytesIn:    cr.n,
			BytesOut:   cw.bytes,
			ErrMessage: errMsg,
			StreamType: classifyStream(cw.Header().Get("Content-Type")),
		})
	})
}

// parseProcedurePath splits a Connect URL of the shape
// `/<package>.<service>/<rpc>` into its trailing service + rpc
// components. Returns ("", path) for anything that doesn't match
// (e.g. UI requests to "/", static asset paths) — those still get
// recorded, just under the raw path so they're visible.
func parseProcedurePath(p string) (string, string) {
	if !strings.HasPrefix(p, "/") {
		return "", p
	}
	rest := p[1:]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "", p
	}
	fqn := rest[:slash]
	procedure := rest[slash+1:]

	service := fqn
	if dot := strings.LastIndexByte(fqn, '.'); dot >= 0 {
		service = fqn[dot+1:]
	}
	return service, procedure
}

// classifyStatus maps an HTTP status code to a Connect-friendly
// string + optional error message. 200 = "ok"; everything else
// reports the status and a short message so the operator can scan
// the table without expanding each row.
func classifyStatus(code int) (string, string) {
	if code == 0 || code == http.StatusOK {
		return "ok", ""
	}
	switch code {
	case http.StatusUnauthorized:
		return "unauthenticated", "401 Unauthorized"
	case http.StatusForbidden:
		return "permission_denied", "403 Forbidden"
	case http.StatusNotFound:
		return "not_found", "404 Not Found"
	case http.StatusRequestTimeout:
		return "deadline_exceeded", "408 Request Timeout"
	case http.StatusTooManyRequests:
		return "resource_exhausted", "429 Too Many Requests"
	case http.StatusInternalServerError:
		return "internal", "500 Internal Server Error"
	case http.StatusServiceUnavailable:
		return "unavailable", "503 Service Unavailable"
	default:
		return "http_" + itoa(code), http.StatusText(code)
	}
}

// classifyStream guesses unary vs server-stream from the response
// Content-Type. Connect server-streaming uses
// "application/connect+json" / "application/connect+proto" or
// "application/grpc-web*"; unary uses plain "application/json" or
// "application/proto".
const (
	streamTypeUnary  = "unary"
	streamTypeStream = "server-stream"
)

func classifyStream(ct string) string {
	switch {
	case strings.Contains(ct, "connect+"):
		return streamTypeStream
	case strings.Contains(ct, "grpc-web"):
		return streamTypeStream
	case strings.Contains(ct, "application/grpc"):
		return streamTypeStream
	default:
		return streamTypeUnary
	}
}

func isStreamRPCCallsPath(p string) bool {
	return strings.HasSuffix(p, "/StreamRPCCalls")
}

// itoa keeps the middleware free of strconv import bloat for the
// one place that needs it.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// countingResponseWriter wraps http.ResponseWriter so the
// middleware can capture status code + bytes written. The
// embedded ResponseWriter forwards Header() / WriteHeader() /
// Write() without reimplementing them.
type countingResponseWriter struct {
	http.ResponseWriter
	bytes  int64
	status int
}

func (c *countingResponseWriter) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *countingResponseWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		// http.ResponseWriter implicitly WriteHeader(200) on first
		// Write. Mirror that here so Status reads back "ok".
		c.status = http.StatusOK
	}
	n, err := c.ResponseWriter.Write(b)
	c.bytes += int64(n)
	return n, err
}

// Flush is implemented so server-streaming Connect handlers can
// keep flushing chunks; the embedded ResponseWriter usually
// implements it but Go's stdlib quirk is that an explicit method
// is the safe path.
func (c *countingResponseWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http2 / h2c introspect the real writer. Without it,
// stream upgrades fail with "ResponseWriter does not implement
// http.Hijacker / http.Flusher".
func (c *countingResponseWriter) Unwrap() http.ResponseWriter {
	return c.ResponseWriter
}

// countingReadCloser wraps a request body to track bytes read.
type countingReadCloser struct {
	io.ReadCloser
	n int64
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n += int64(n)
	return n, err
}
