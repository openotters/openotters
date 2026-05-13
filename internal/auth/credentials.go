package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CredentialsPath returns the canonical location of the operator
// credentials file: $HOME/.otters/credentials.json. Caller may
// override via $OTTERS_CREDENTIALS_FILE for tests / multi-daemon
// dev setups.
func CredentialsPath() (string, error) {
	if override := os.Getenv("OTTERS_CREDENTIALS_FILE"); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("auth: locate home: %w", err)
	}
	return filepath.Join(home, ".otters", "credentials.json"), nil
}

// CredentialsFile is the on-disk shape. Keyed by URL so a single dev
// host running multiple daemons (different --http-addr ports) gets
// separate entries. Future iterations may add per-endpoint
// metadata (refresh token, expires_at, profile name, …) — keeping a
// per-endpoint object instead of a flat string future-proofs that.
type CredentialsFile struct {
	Endpoints map[string]EndpointCredentials `json:"endpoints"`
}

type EndpointCredentials struct {
	Token    string    `json:"token"`
	IssuedAt time.Time `json:"issued_at"`
}

// LoadCredentials reads the credentials file. Returns an empty (not
// nil) CredentialsFile when the file doesn't exist — caller can
// add an entry and SaveCredentials without an existence check.
func LoadCredentials() (*CredentialsFile, error) {
	path, err := CredentialsPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &CredentialsFile{Endpoints: map[string]EndpointCredentials{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: read credentials: %w", err)
	}
	var cf CredentialsFile
	if err = json.Unmarshal(raw, &cf); err != nil {
		return nil, fmt.Errorf("auth: decode credentials: %w", err)
	}
	if cf.Endpoints == nil {
		cf.Endpoints = map[string]EndpointCredentials{}
	}
	return &cf, nil
}

// SaveCredentials writes the file atomically with mode 0600. Creates
// parent directory if needed (mode 0700 — the directory holds
// secrets too).
func SaveCredentials(cf *CredentialsFile) error {
	path, err := CredentialsPath()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("auth: mkdir credentials: %w", err)
	}
	body, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: encode credentials: %w", err)
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("auth: write credentials tmp: %w", err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("auth: rename credentials: %w", err)
	}
	return nil
}

// SocketURL canonicalizes a unix socket path into the URL form used
// as the credentials.json key (and as OTTERSD_URL in agent envs).
// "unix://" + absolute path. Centralised so every caller —
// daemon bootstrap, CLI flag handling, runtime client — agrees on
// the lookup string.
func SocketURL(path string) string {
	return "unix://" + path
}

// EnsureOperatorToken upserts an entry for `endpoint` with a freshly-
// minted operator token. Returns (created, token, error): created=true
// means a new token was written and the daemon logs that so the
// operator notices.
//
// Existing entries are reused ONLY when the stored token still
// validates against the current signing key. If validation fails
// (different daemon owns the same endpoint, key rotation, corruption),
// the entry is overwritten — silently handing back a token that the
// daemon's own interceptor would reject would cause every browser /
// CLI request from autoBearer to 401.
//
// `endpoint` may be a TCP URL (http://127.0.0.1:5050) OR a unix
// socket URL (unix:///tmp/otters-dev.sock). Both are stored side by
// side so a daemon serving both listeners has one entry per surface.
func EnsureOperatorToken(endpoint string, signingKey []byte) (bool, string, error) {
	cf, err := LoadCredentials()
	if err != nil {
		return false, "", err
	}
	if existing, ok := cf.Endpoints[endpoint]; ok && existing.Token != "" {
		if _, vErr := Validate(signingKey, existing.Token, nil); vErr == nil {
			return false, existing.Token, nil
		}
		// Existing token doesn't validate — fall through to re-mint.
	}

	token, _, err := IssueOperator(signingKey)
	if err != nil {
		return false, "", fmt.Errorf("auth: issue operator token: %w", err)
	}
	cf.Endpoints[endpoint] = EndpointCredentials{
		Token:    token,
		IssuedAt: time.Now().UTC(),
	}
	if err = SaveCredentials(cf); err != nil {
		return false, "", err
	}
	return true, token, nil
}

// LookupToken returns the token persisted for `endpoint`, or "" when
// no entry exists. Used by the CLI to find its bearer credential
// when targeting a TCP daemon.
func LookupToken(endpoint string) (string, error) {
	cf, err := LoadCredentials()
	if err != nil {
		return "", err
	}
	if entry, ok := cf.Endpoints[endpoint]; ok {
		return entry.Token, nil
	}
	return "", nil
}
