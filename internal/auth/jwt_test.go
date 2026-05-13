// White-box test: pins behaviour of unexported helpers and reaches
// into the issue / parse paths to assert claim shapes.
//
//nolint:testpackage // intentional white-box access to private jwt internals
package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// neverRevoked is the no-op revocation check used when a test
// doesn't care about the revocation path.
func neverRevoked(string) (bool, error) { return false, nil }

func TestIssueOperator_RoundTrips(t *testing.T) {
	t.Parallel()
	key := []byte("test-key-32-bytes-aaaaaaaaaaaaaa")

	tok, jti, err := IssueOperator(key)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}
	if jti == "" {
		t.Fatal("jti must be non-empty")
	}

	claims, err := Validate(key, tok, neverRevoked)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if claims.Issuer != IssuerOperator {
		t.Errorf("Issuer = %q, want %q", claims.Issuer, IssuerOperator)
	}
	if claims.AgentRef != "" {
		t.Errorf("operator token AgentRef = %q, want empty", claims.AgentRef)
	}
	if claims.ID != jti {
		t.Errorf("claims.ID = %q, want %q", claims.ID, jti)
	}
}

func TestIssueAgent_BindsAgentRef(t *testing.T) {
	t.Parallel()
	key := []byte("test-key-32-bytes-aaaaaaaaaaaaaa")

	tok, _, err := IssueAgent(key, "agent-uuid-123")
	if err != nil {
		t.Fatalf("IssueAgent: %v", err)
	}
	claims, err := Validate(key, tok, neverRevoked)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if claims.AgentRef != "agent-uuid-123" {
		t.Errorf("AgentRef = %q, want %q", claims.AgentRef, "agent-uuid-123")
	}
	if claims.Issuer != IssuerAgent {
		t.Errorf("Issuer = %q, want %q", claims.Issuer, IssuerAgent)
	}
}

func TestIssueAgent_RequiresID(t *testing.T) {
	t.Parallel()
	_, _, err := IssueAgent([]byte("k"), "")
	if err == nil {
		t.Fatal("IssueAgent with empty id should error")
	}
}

func TestValidate_RejectsTamperedSignature(t *testing.T) {
	t.Parallel()
	key := []byte("test-key-32-bytes-aaaaaaaaaaaaaa")
	tok, _, _ := IssueOperator(key)

	// JWT shape is <header>.<payload>.<signature>; the signature
	// segment is base64url-encoded HMAC-SHA256 bytes. We mutate the
	// front of the signature (a stretch of "A"s = all-zero bytes)
	// to guarantee a mismatch.
	//
	// Naive "flip the last char" doesn't work reliably: base64url's
	// terminating character only encodes 4 useful bits for a 32-byte
	// HMAC, so swapping to 'A' or 'B' frequently produces the same
	// decoded bytes — a flaky test we lived with for a while.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("issued token does not look like a JWT: %q", tok)
	}
	if len(parts[2]) < 16 {
		t.Fatalf("signature segment is suspiciously short: %q", parts[2])
	}
	parts[2] = strings.Repeat("A", 16) + parts[2][16:]
	tampered := strings.Join(parts, ".")

	_, err := Validate(key, tampered, neverRevoked)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("err = %v, want ErrTokenInvalid", err)
	}
}

func TestValidate_RejectsExpired(t *testing.T) {
	t.Parallel()
	key := []byte("test-key-32-bytes-aaaaaaaaaaaaaa")

	// Build a token expired an hour ago by hand — IssueOperator
	// always uses a future exp, so we bypass it.
	c := Claims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer:    IssuerOperator,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		ID:        "j-expired",
	}}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(key)
	if err != nil {
		t.Fatalf("sign expired: %v", err)
	}

	_, err = Validate(key, tok, neverRevoked)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("expired err = %v, want ErrTokenInvalid", err)
	}
}

func TestValidate_RejectsRevoked(t *testing.T) {
	t.Parallel()
	key := []byte("test-key-32-bytes-aaaaaaaaaaaaaa")
	tok, jti, _ := IssueOperator(key)

	revoked := func(j string) (bool, error) { return j == jti, nil }
	_, err := Validate(key, tok, revoked)
	if !errors.Is(err, ErrTokenRevoked) {
		t.Errorf("err = %v, want ErrTokenRevoked", err)
	}

	// And confirm a different jti still passes (sanity — no global state).
	other, _, _ := IssueOperator(key)
	if _, vErr := Validate(key, other, revoked); vErr != nil {
		t.Errorf("unrelated token validate: %v", vErr)
	}
}

func TestValidate_RejectsAlgorithmConfusion(t *testing.T) {
	t.Parallel()
	key := []byte("test-key-32-bytes-aaaaaaaaaaaaaa")

	// Hand-build an "alg=none" token — old JWT libs have famously
	// accepted these. We pin HS256 in jwt.WithValidMethods so the
	// validator must refuse.
	noneTok := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: IssuerOperator,
			ID:     "j-none",
		},
	})
	signed, err := noneTok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	// Sanity: the none-signed token ends with a trailing dot (empty
	// signature segment). Confirms we actually built an alg=none
	// token and not e.g. a fallback HS256 one.
	if !strings.HasSuffix(signed, ".") {
		t.Fatalf("expected none-signed token (trailing '.'); got %q", signed)
	}

	_, err = Validate(key, signed, neverRevoked)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("alg=none err = %v, want ErrTokenInvalid", err)
	}
}
