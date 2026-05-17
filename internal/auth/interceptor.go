package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/openotters/openotters/internal/observability"
)

// ctxKey is unexported so test helpers / future packages can't
// accidentally collide. Single key today: the parsed claims attached
// by JWTInterceptor.
type ctxKey int

const claimsKey ctxKey = iota

// ClaimsFromContext returns the JWT claims attached by JWTInterceptor.
// Returns nil when no JWTInterceptor ran on the request (shouldn't
// happen in production — both listeners wrap the handler).
func ClaimsFromContext(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsKey).(*Claims)
	return c
}

// JWTInterceptor wraps an http.Handler so every request must carry a
// valid Bearer token. Mounted on BOTH the unix-socket and TCP
// listeners — there is no implicit-trust path. The agent reaches
// the daemon through a bind-mounted unix socket; CLI / UI /
// external callers reach it over TCP. All present a JWT.
//
// On success the parsed claims are stashed in ctx; failure
// short-circuits with 401 + a JSON error body so Connect clients
// surface a usable Unauthenticated.
type JWTInterceptor struct {
	Key       []byte
	IsRevoked func(jti string) (bool, error)
}

// Wrap returns the http.Handler the daemon mounts on every listener.
func (i *JWTInterceptor) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := bearerToken(r)
		if err != nil {
			writeUnauthenticated(w, err)
			return
		}

		claims, err := Validate(i.Key, raw, i.IsRevoked)
		if err != nil {
			writeUnauthenticated(w, err)
			return
		}

		// Fill in the per-request CallInfo the observability
		// middleware injected upstream so the recorder logs the
		// authenticated caller. operator tokens have empty
		// AgentRef; agent tokens carry the per-agent ref string.
		if info := observability.CallInfoFromContext(r.Context()); info != nil {
			if claims.AgentRef != "" {
				info.Caller = "agent:" + claims.AgentRef
				info.AgentRef = claims.AgentRef
			} else {
				info.Caller = "operator"
			}
		}

		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken pulls the token out of the Authorization header.
// Strict shape: `Authorization: Bearer <jwt>` — anything else returns
// ErrTokenInvalid so the response is uniform regardless of the
// failure mode.
func bearerToken(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", ErrTokenInvalid
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", ErrTokenInvalid
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" {
		return "", ErrTokenInvalid
	}
	return tok, nil
}

// writeUnauthenticated emits a 401 with a Connect-compatible JSON
// error body. Connect clients surface this as the Unauthenticated
// code; raw HTTP clients see a meaningful Status and message.
func writeUnauthenticated(w http.ResponseWriter, err error) {
	reason := "missing or invalid bearer token"
	if errors.Is(err, ErrTokenRevoked) {
		reason = "token has been revoked"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	// Connect's error JSON shape: {code, message}. Lower-case code
	// matches Connect's wire format.
	_, _ = w.Write([]byte(`{"code":"unauthenticated","message":"` + reason + `"}`))
}
