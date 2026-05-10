// Package auth holds JWT issuance/validation, the credentials.json
// reader the CLI uses to find its operator token, and the HTTP
// middleware that wraps Connect handlers with bearer-token enforcement
// (TCP) or implicit admin trust (unix socket).
//
// The signing key lives in the daemon's existing SQLite via the
// secrets table — same backup story as the rest of the daemon's
// state. Symmetric HMAC-HS256 is enough since validation only ever
// happens in the daemon process; an asymmetric scheme would just be
// more code without a use case yet.
package auth

import (
	"context"
	"crypto/rand"
	"fmt"
)

// SigningKeyName is the secrets-table key under which the signing
// key is persisted. Centralised here so every caller (loader,
// hypothetical rotation flow) refers to the same string.
const SigningKeyName = "jwt_signing_key"

// SecretStore is the slice of StateStore that auth needs. Decoupled
// into an interface so the package can be unit-tested without
// standing up the whole daemon DB schema.
type SecretStore interface {
	GetSecret(ctx context.Context, name string) ([]byte, error)
	PutSecret(ctx context.Context, name string, value []byte) error
}

// LoadOrCreateSigningKey returns the daemon's HS256 signing key,
// generating + persisting a fresh 32-byte secret on first call. Idempotent
// on every subsequent boot — the same daemon always gets the same key
// (so existing tokens continue to validate after a restart).
//
// 32 bytes (256 bits) matches HS256's recommended minimum; less than
// that and the JWT lib still works but you're below the brute-force
// floor the algorithm assumes.
func LoadOrCreateSigningKey(ctx context.Context, store SecretStore) ([]byte, error) {
	existing, err := store.GetSecret(ctx, SigningKeyName)
	if err != nil {
		return nil, fmt.Errorf("auth: read signing key: %w", err)
	}
	if len(existing) > 0 {
		return existing, nil
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("auth: generate signing key: %w", err)
	}

	if err := store.PutSecret(ctx, SigningKeyName, key); err != nil {
		return nil, fmt.Errorf("auth: persist signing key: %w", err)
	}
	return key, nil
}
