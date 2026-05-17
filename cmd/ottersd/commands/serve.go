package commands

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/merlindorin/go-shared/pkg/cmd"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/openotters/openotters/api/v1/daemonv1connect"
	"github.com/openotters/openotters/internal"
	"github.com/openotters/openotters/internal/auth"
	"github.com/openotters/openotters/internal/observability"
	"github.com/openotters/openotters/internal/webui"
)

type Serve struct {
	SocketPath     string   `help:"Unix socket path" default:""`
	Runtime        string   `help:"Path to a local runtime binary (skips pulling the runtime image from OCI)" default:""`
	RegistryAddr   string   `help:"TCP bind address for the embedded OCI registry (overrides OTTERS_REGISTRY_ADDR)" default:""`
	HTTPAddr       string   `help:"TCP listener address for the Connect/gRPC-Web API and embedded web UI. Loopback-only by default; non-loopback requires --auth-token." default:"127.0.0.1:5500"`
	NoHTTP         bool     `name:"no-http" help:"Disable the TCP listener; only the Unix socket (CLI) is exposed." default:"false"`
	NoUI           bool     `name:"no-ui" help:"Don't serve the embedded web UI on the TCP listener; only the Connect/gRPC API is reachable." default:"false"`
	UIPath         string   `name:"ui-path" help:"Serve the web UI from this directory instead of the binary's embedded build. Useful for running a local Next.js export." default:""`
	AllowedOrigins []string `help:"CORS Access-Control-Allow-Origin values for the TCP listener (repeatable)." default:"http://localhost:3000,http://localhost:3030"`
	// --auth-token (legacy static bearer) was removed when JWT auth
	// landed. Operator tokens are now minted at first daemon boot and
	// stored in ~/.otters/credentials.json (mode 0600); see
	// internal/auth/credentials.go.
	MaxConcurrent   int           `help:"Maximum agents allowed to run concurrently in the pool." default:"10"`
	BackoffBase     time.Duration `help:"Auto-restart backoff base delay for agents in init/pull/model_error. Schedule is base × 2^attempt, capped by --backoff-cap." default:"1s"`
	BackoffCap      time.Duration `help:"Maximum delay between auto-restart attempts." default:"30s"`
	ShutdownTimeout time.Duration `help:"Graceful shutdown deadline for in-flight HTTP/Connect requests when SIGINT fires." default:"5s"`

	// Executor selects the backend agents run on. system spawns the
	// runtime as a host subprocess (current default; works on any
	// platform). docker runs each agent as a Docker container with
	// the runtime + BIN tools mounted as OCI image-mounts (requires
	// Docker Engine ≥ 25 with the containerd snapshotter). Honours
	// the OTTERSD_EXECUTOR env var so users can pin a default in
	// their shell profile without remembering the flag.
	Executor string `enum:"system,docker" default:"system" env:"OTTERSD_EXECUTOR" help:"Agent runtime backend: 'system' (host subprocess) or 'docker' (container). Honours OTTERSD_EXECUTOR."`
}

//nolint:funlen // single-shot daemon bootstrap reads more clearly straight-through
func (d *Serve) Run(ctx context.Context, common *cmd.Commons, sqlite *cmd.SQLite) error {
	logger := common.MustLogger().Named("daemon")

	socketPath := d.SocketPath
	if socketPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}

		socketPath = filepath.Join(home, ".otters", "otters.sock")
	}

	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}

	_ = os.Remove(socketPath)

	providers, err := internal.LoadProviders(internal.WithProvidersLogger(logger.Named("providers")))
	if err != nil {
		return fmt.Errorf("loading providers: %w", err)
	}

	providers.Each(func(p *internal.ProviderConfig) {
		logger.Info("provider loaded",
			zap.String("name", p.Name),
			zap.String("api_base", p.APIBase),
			zap.Int("models", len(p.Models)),
		)
	})

	// The embedded oras registry is the system executor's storage
	// backend. The docker executor uses Docker's image store
	// directly (via docker.Store + cli.ImageLoad / ImageSave), so
	// the HTTP server has no callers — skip starting it to avoid
	// the bind-port footprint and the surprise of stale state
	// surviving an executor switch.
	var reg *internal.EmbeddedRegistry
	if d.Executor != "docker" {
		reg = internal.NewEmbeddedRegistry(logger, internal.WithRegistryAddr(d.RegistryAddr))
		if err = reg.Start(ctx); err != nil {
			return fmt.Errorf("starting embedded registry: %w", err)
		}

		defer reg.Stop()
	}

	if sqlite.Path == ":memory:" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return homeErr
		}

		sqlite.Path = filepath.Join(home, ".otters", "daemon.db")
	}

	db, err := sqlite.Open()
	if err != nil {
		return fmt.Errorf("opening state database: %w", err)
	}
	defer db.Close()

	state, err := internal.NewStateStore(ctx, db)
	if err != nil {
		return fmt.Errorf("creating state store: %w", err)
	}

	// Load the JWT signing key before NewDaemon so it can be passed
	// via WithSigningKey — CreateAgent uses it to mint per-agent
	// tokens. Same key is used by the JWT interceptor on the TCP
	// listener (constructed lower down) so issued tokens validate.
	signingKey, err := auth.LoadOrCreateSigningKey(ctx, state)
	if err != nil {
		return fmt.Errorf("loading signing key: %w", err)
	}

	// publicURL is the daemon's TCP endpoint — needed by the docker
	// executor to point its agents at the right host:port.
	// auto-rewritten 127.0.0.1 → host.docker.internal at spawn
	// time inside Daemon.agentReachableURL.
	var daemonPublicURL string
	if !d.NoHTTP && d.HTTPAddr != "" {
		daemonPublicURL = publicURL(d.HTTPAddr)
	}

	daemonOpts := []internal.DaemonOption{
		internal.WithSocket(socketPath),
		internal.WithSigningKey(signingKey),
		internal.WithPublicURL(daemonPublicURL),
		internal.WithBuildInfo(
			common.Version.Version(),
			common.Version.Commit(),
			common.Version.Date(),
		),
		internal.WithPoolMaxConcurrent(d.MaxConcurrent),
		internal.WithPoolBackoffBase(d.BackoffBase),
		internal.WithPoolBackoffCap(d.BackoffCap),
		internal.WithShutdownTimeout(d.ShutdownTimeout),
		internal.WithExecutor(d.Executor),
	}

	if d.Runtime != "" {
		if _, statErr := os.Stat(d.Runtime); statErr != nil {
			return fmt.Errorf("runtime binary %s: %w", d.Runtime, statErr)
		}

		daemonOpts = append(daemonOpts, internal.WithLocalRuntime(d.Runtime))
		logger.Info("using local runtime binary", zap.String("path", d.Runtime))
	}

	dm := internal.NewDaemon(providers, reg, state, logger, daemonOpts...)
	if runErr := dm.Run(ctx); runErr != nil {
		return runErr
	}

	if restoreErr := dm.Restore(ctx); restoreErr != nil {
		logger.Warn("failed to restore agents", zap.Error(restoreErr))
	}

	// Async-jobs Boot: orphan rows still in `running` from a prior
	// process, redispatch any `pending`. Runs after agent Restore so
	// any boot-time delivery has the agents back online.
	if jobsErr := dm.AsyncJobs().Boot(ctx); jobsErr != nil {
		logger.Warn("async-jobs boot replay failed", zap.Error(jobsErr))
	}

	// JWT interceptor for the TCP listener — validates Bearer tokens
	// against the signing key loaded above (same key the daemon used
	// to mint per-agent JWTs at CreateAgent). Unix listener wraps
	// with WithUnixTrust below to bypass validation.
	jwtIcp := &auth.JWTInterceptor{
		Key:       signingKey,
		IsRevoked: func(jti string) (bool, error) { return state.IsRevoked(ctx, jti) },
	}

	// Single Connect-Go handler serves gRPC, gRPC-Web, and Connect from
	// the same code path. The protocol is content-type-detected per
	// request, so the CLI (gRPC over h2c) and the browser (Connect/JSON
	// or gRPC-Web binary) hit the same handler implementation.
	connectPath, connectHandler := daemonv1connect.NewRuntimeHandler(
		internal.NewRuntimeHandler(dm, providers),
	)

	// Unix-socket mux: API only. The CLI never asks for `/`, so we
	// keep the UI handler off this transport.
	apiMux := http.NewServeMux()
	apiMux.Handle(connectPath, connectHandler)

	// JWT auth is enforced on EVERY listener — including the unix
	// socket. The agent's runtime reaches the daemon through the
	// same socket (bind-mounted into the container by the executor),
	// so there's no "trust by transport" path; every caller proves
	// identity via Bearer JWT.
	//
	// Order matters: jwt.Wrap must be INSIDE h2c.NewHandler so the
	// gRPC client's HTTP/2 upgrade happens before our middleware
	// inspects headers — without this the failure response is
	// HTTP/1.1 and the client errors with "frame too large" before
	// surfacing the Unauthenticated.
	//
	// The observability middleware wraps OUTSIDE the JWT
	// interceptor so the RPC monitor sees auth-failed calls too
	// (Caller "anonymous" + Status "unauthenticated"). JWT writes
	// the resolved Caller into the per-request CallInfo struct on
	// success.
	recorderMw := observability.NewMiddleware(dm.Recorder())
	apiHandler := recorderMw.Wrap(jwtIcp.Wrap(apiMux))

	// h2c so gRPC clients (the CLI) can negotiate HTTP/2 cleartext
	// over the Unix socket and (if enabled) the TCP listener without
	// a TLS handshake.
	unixRoot := h2c.NewHandler(apiHandler, &http2.Server{})

	lc := net.ListenConfig{}
	unixL, err := lc.Listen(ctx, "unix", socketPath)
	if err != nil {
		return err
	}

	unixSrv := &http.Server{Handler: unixRoot, ReadHeaderTimeout: 30 * time.Second}

	logger.Info("daemon listening", zap.String("socket", socketPath))

	// Bootstrap the operator token for the unix socket — the CLI's
	// default dev flow (`task client:dev`) hits this URL, so the
	// token MUST be written before the first CLI invocation.
	socketURL := auth.SocketURL(socketPath)
	if created, _, ensureErr := auth.EnsureOperatorToken(socketURL, signingKey); ensureErr != nil {
		logger.Warn("operator-token bootstrap failed (unix)", zap.Error(ensureErr))
	} else if created {
		credPath, _ := auth.CredentialsPath()
		logger.Info("operator token written",
			zap.String("endpoint", socketURL),
			zap.String("credentials", credPath))
	}

	// TCP listener — exposed by default so the embedded web UI works
	// out of the box. Pass --no-http to disable; loopback-only by
	// default so the listener stays inside the box without --auth-token.
	var tcpSrv *http.Server

	if !d.NoHTTP && d.HTTPAddr != "" {
		srv, startErr := d.startTCPListener(ctx, logger, connectPath, connectHandler, jwtIcp, signingKey, dm.Recorder())
		if startErr != nil {
			return startErr
		}

		tcpSrv = srv
	}

	// Shutdown goroutine: the parent ctx has fired, so we can't use it
	// as the parent of the bounded shutdown deadline (it would be
	// already cancelled). context.Background is intentional here.
	//nolint:gosec // G118: ctx is the cancelled trigger; shutdown needs a fresh deadline.
	go func() {
		<-ctx.Done()
		logger.Info("shutting down daemon")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), d.ShutdownTimeout)
		defer cancel()

		// Drain async jobs first so their cancellation paths can settle
		// before the gRPC listener stops accepting traffic.
		if jobsErr := dm.AsyncJobs().Shutdown(shutdownCtx); jobsErr != nil {
			logger.Warn("async-jobs shutdown timed out", zap.Error(jobsErr))
		}

		_ = unixSrv.Shutdown(shutdownCtx)

		if tcpSrv != nil {
			_ = tcpSrv.Shutdown(shutdownCtx)
		}
	}()

	if serveErr := unixSrv.Serve(unixL); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}

	return nil
}

// startTCPListener builds the TCP-side mux (Connect at its canonical
// path, an `/api/...` alias for same-origin browser calls, and the
// embedded UI on `/`), wraps it in h2c + auth + CORS, and begins
// serving in a goroutine. Returns the *http.Server so the caller can
// orchestrate shutdown alongside the Unix listener.
func (d *Serve) startTCPListener(
	ctx context.Context,
	logger *zap.Logger,
	connectPath string,
	connectHandler http.Handler,
	jwtIcp *auth.JWTInterceptor,
	signingKey []byte,
	recorder *observability.Recorder,
) (*http.Server, error) {
	// JWT replaces the prior --auth-token static-bearer mechanism.
	// Binding to non-loopback no longer requires a flag opt-in:
	// the interceptor enforces a valid token on every request, so
	// "exposed to the LAN" and "exposed to localhost" have the same
	// auth surface.

	// TCP mux: the API at its canonical Connect path, plus an
	// `/api/...` alias so a same-origin browser can reach the daemon
	// without a CORS preflight (the embedded UI is served from the
	// same listener). The web UI catches everything else; concrete
	// paths win in http.ServeMux's matching, so connectPath and
	// `/api/<connectPath>` both beat `/`.
	endpoint := publicURL(d.HTTPAddr)

	tcpMux := http.NewServeMux()
	// JWT-wrap each Connect mount — but NOT the UI handler, which
	// serves static assets that the browser fetches without an auth
	// header. Browser → API requests go through the proxied /api
	// path, where autoBearer SERVER-SIDE injects the operator token
	// when none is present (so the browser never holds a credential
	// — same-origin XSS or DevTools snooping can't lift one), then
	// the JWT interceptor validates. Externally-issued tokens (CLI,
	// agents, future scoped tokens) come with their own Authorization
	// header which autoBearer leaves untouched, so they validate
	// against their own claims.
	//
	// Mint the operator token here (idempotent — bootstrap above may
	// already have written it on first boot; Ensure returns the
	// existing one if so). Empty token → autoBearer no-ops, every
	// browser request fails 401 with a clean error.
	_, opTok, _ := auth.EnsureOperatorToken(endpoint, signingKey)
	apiInner := autoBearer(opTok, jwtIcp.Wrap(connectHandler))
	apiAlias := autoBearer(opTok, jwtIcp.Wrap(http.StripPrefix("/api", connectHandler)))

	// Wrap the API mounts with the recorder middleware (outside
	// auth) so the RPC monitor sees every call hitting this
	// listener. The UI handler at "/" stays unrecorded — static
	// asset fetches aren't RPCs and would flood the buffer.
	if recorder != nil {
		recorderMw := observability.NewMiddleware(recorder)
		apiInner = recorderMw.Wrap(apiInner)
		apiAlias = recorderMw.Wrap(apiAlias)
	}

	// Same h2c-vs-jwt ordering rule as the unix listener: jwt INSIDE
	// h2c so the HTTP/2 upgrade is established before middleware
	// touches headers.
	tcpMux.Handle(connectPath, apiInner)
	tcpMux.Handle("/api"+connectPath, apiAlias)

	if !d.NoUI {
		tcpMux.Handle("/", webui.Handler(d.UIPath))
	}

	wrapped := h2c.NewHandler(tcpMux, &http2.Server{})

	wrapped = withCORS(wrapped, d.AllowedOrigins)
	srv := &http.Server{Handler: wrapped, ReadHeaderTimeout: 30 * time.Second}

	lc := net.ListenConfig{}

	tcpL, err := lc.Listen(ctx, "tcp", d.HTTPAddr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", d.HTTPAddr, err)
	}

	// Operator-token bootstrap (now using the same `endpoint` value
	// as the autoBearer wiring above so they share the credentials
	// entry — first call writes, second is idempotent return).
	if created, _, ensureErr := auth.EnsureOperatorToken(endpoint, signingKey); ensureErr != nil {
		logger.Warn("operator-token bootstrap failed", zap.Error(ensureErr))
	} else if created {
		credPath, _ := auth.CredentialsPath()
		logger.Info("operator token written",
			zap.String("endpoint", endpoint),
			zap.String("credentials", credPath))
	}

	go func() {
		logger.Info("daemon TCP listener",
			zap.String("addr", d.HTTPAddr),
			zap.Bool("ui", !d.NoUI),
		)

		if serveErr := srv.Serve(tcpL); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("TCP serve error", zap.Error(serveErr))
		}
	}()

	return srv, nil
}

// autoBearer injects the operator token as the Authorization header
// when the request arrives without one. Mounted on the TCP /api/*
// path so the embedded UI (served from the same listener) can call
// the daemon without holding a credential client-side. Externally
// authenticated callers (CLI, agents, scoped tokens) always present
// their own Authorization header and bypass injection — their token
// is what gets validated.
//
// Effective trust model on the TCP listener: anyone who can reach
// the port without auth is treated as the operator. Loopback bind
// (default --http-addr 127.0.0.1:…) makes this safe — the same
// boundary as Docker's daemon.sock. A non-loopback bind should pair
// with --no-ui so this path doesn't activate.
func autoBearer(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	bearer := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", bearer)
		}
		next.ServeHTTP(w, r)
	})
}

// publicURL turns an --http-addr value (e.g. "127.0.0.1:5050") into
// the URL agents and CLI clients should dial. Always http (no TLS in
// v1). Empty host treated as 127.0.0.1 so a "":5050 binding still
// produces a working URL for the credentials file.
func publicURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + host + ":" + port
}

// withCORS adds the minimum CORS dance the browser needs to call
// Connect endpoints from a different origin. We match exact origins
// from the allowlist; "*" is intentionally not supported because
// `Authorization` headers are involved on the auth-token path.
func withCORS(next http.Handler, allowed []string) http.Handler {
	allowSet := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		allowSet[strings.TrimSpace(o)] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if _, ok := allowSet[origin]; ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		}

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers",
				"Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms, Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)

			return
		}

		next.ServeHTTP(w, r)
	})
}
