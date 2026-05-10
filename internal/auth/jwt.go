package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Issuer string distinguishes operator tokens from agent tokens. The
// daemon's interceptor accepts both with no behavioural difference
// today; the next iteration adds per-issuer scope checks (operator
// = admin everywhere, agent = bound to its own AgentRef).
const (
	IssuerOperator = "ottersd"
	IssuerAgent    = "ottersd:agent"
)

// Claims is the daemon's JWT payload. Stays minimal in this iteration
// so future additions (e.g. a Scopes []string slice) can land without
// a wire-format break — JSON unmarshalling tolerates unknown fields,
// and missing fields decode as zero values.
type Claims struct {
	jwt.RegisteredClaims
	// AgentRef is set on agent tokens — empty for operator tokens.
	// The next iteration's per-agent binding reads this to force
	// the request's agent_ref server-side.
	AgentRef string `json:"agent_ref,omitempty"`
}

// Default token lifetimes. Operator tokens are short enough that a
// human can be expected to rotate them annually; agent tokens are
// long because the daemon revokes them via jti at agent removal,
// not by waiting for exp.
const (
	OperatorTokenTTL = 365 * 24 * time.Hour
	AgentTokenTTL    = 10 * 365 * 24 * time.Hour
)

// IssueOperator mints an operator token and returns it alongside its
// jti so callers can record the jti for future revocation.
func IssueOperator(key []byte) (token, jti string, err error) {
	return issue(key, IssuerOperator, "")
}

// IssueAgent mints a token bound to an agent's UUID and returns it
// alongside its jti. Caller persists both — token lands in
// agents.token, jti in agents.token_jti so RemoveAgent can revoke.
func IssueAgent(key []byte, agentID string) (token, jti string, err error) {
	if agentID == "" {
		return "", "", errors.New("IssueAgent: agentID is required")
	}
	return issue(key, IssuerAgent, agentID)
}

func issue(key []byte, issuer, agentRef string) (string, string, error) {
	if len(key) == 0 {
		return "", "", errors.New("issue: signing key is empty")
	}
	jti := uuid.NewString()
	now := time.Now()
	ttl := OperatorTokenTTL
	if issuer == IssuerAgent {
		ttl = AgentTokenTTL
	}
	c := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        jti,
		},
		AgentRef: agentRef,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	signed, err := tok.SignedString(key)
	if err != nil {
		return "", "", fmt.Errorf("sign jwt: %w", err)
	}
	return signed, jti, nil
}

// Validate parses + verifies a token. Returns the decoded claims OR a
// typed error so the interceptor can map them to wire codes:
//   - ErrTokenInvalid  → Unauthenticated (signature, structure, exp)
//   - ErrTokenRevoked  → Unauthenticated (jti present in revocation list)
//
// isRevoked is invoked once per validate AFTER the signature passes,
// so a forged token never touches the revocation store. Caller
// supplies the closure to keep the package free of a SQL dep.
func Validate(key []byte, raw string, isRevoked func(jti string) (bool, error)) (*Claims, error) {
	if raw == "" {
		return nil, ErrTokenInvalid
	}
	if isRevoked == nil {
		isRevoked = func(string) (bool, error) { return false, nil }
	}

	parsed, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (any, error) {
		// Pin the algorithm — without this, an attacker could forge a
		// token signed with "none" or RS256 and our HS256-validating
		// path would happily accept it (the alg-confusion class).
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected alg %v", t.Header["alg"])
		}
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !parsed.Valid {
		return nil, ErrTokenInvalid
	}

	claims, ok := parsed.Claims.(*Claims)
	if !ok {
		return nil, ErrTokenInvalid
	}

	revoked, err := isRevoked(claims.ID)
	if err != nil {
		return nil, fmt.Errorf("check revocation for %s: %w", claims.ID, err)
	}
	if revoked {
		return nil, ErrTokenRevoked
	}
	return claims, nil
}

// Sentinel errors. Wrapped by Validate; callers compare with errors.Is.
var (
	ErrTokenInvalid = errors.New("auth: token invalid")
	ErrTokenRevoked = errors.New("auth: token revoked")
)
