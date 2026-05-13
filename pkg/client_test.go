package pkg_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openotters/openotters/pkg"
)

func TestDefaultSocketPath_ResolvesUnderHome(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir unavailable: %v", err)
	}

	want := filepath.Join(home, ".otters", "otters.sock")
	if got := pkg.DefaultSocketPath(); got != want {
		t.Errorf("DefaultSocketPath = %q, want %q", got, want)
	}
}

func TestConnect_HappyPath(t *testing.T) {
	t.Parallel()

	// grpc.NewClient is lazy: this constructs without dialing, so a
	// non-existent socket path is fine for a smoke check that the
	// constructor returns both a non-nil client and a closable conn.
	c, conn, err := pkg.Connect("/tmp/openotters-nonexistent.sock", "")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if c == nil {
		t.Error("Connect returned nil client")
	}

	if conn == nil {
		t.Fatal("Connect returned nil conn")
	}

	if closeErr := conn.Close(); closeErr != nil {
		t.Errorf("conn.Close: %v", closeErr)
	}
}

func TestConnect_RejectsObviouslyBadTarget(t *testing.T) {
	t.Parallel()

	// grpc.NewClient validates the target string. A bare scheme+empty
	// authority should error out before any dial attempt.
	_, _, err := pkg.Connect(strings.Repeat("\x00", 8), "")
	if err == nil {
		t.Fatal("expected error for invalid socket path, got nil")
	}
}

func TestConnectTCP_HappyPath(t *testing.T) {
	t.Parallel()

	// Like Connect, grpc.NewClient is lazy — no actual TCP attempt
	// happens here. The test just covers the URL-parse / address-
	// derivation path and confirms a usable client + closable conn
	// come out the other side.
	c, conn, err := pkg.ConnectTCP("http://127.0.0.1:5500", "test-token")
	if err != nil {
		t.Fatalf("ConnectTCP: %v", err)
	}
	if c == nil {
		t.Error("ConnectTCP returned nil client")
	}
	if conn == nil {
		t.Fatal("ConnectTCP returned nil conn")
	}
	if closeErr := conn.Close(); closeErr != nil {
		t.Errorf("conn.Close: %v", closeErr)
	}
}

func TestConnectTCP_AddsDefaultPort(t *testing.T) {
	t.Parallel()

	// URL with no explicit port — net.SplitHostPort fails on "example.com"
	// alone, so the code path that calls JoinHostPort(addr, "80") fires.
	// Smoke-test that the construction still succeeds.
	c, conn, err := pkg.ConnectTCP("http://example.com", "")
	if err != nil {
		t.Fatalf("ConnectTCP no-port: %v", err)
	}
	if c == nil || conn == nil {
		t.Fatal("ConnectTCP no-port returned nil")
	}
	_ = conn.Close()
}

func TestConnectTCP_RejectsBadURL(t *testing.T) {
	t.Parallel()

	// A URL with raw control bytes makes url.Parse return an error,
	// which ConnectTCP must surface (the early-return path).
	_, _, err := pkg.ConnectTCP("http://"+string([]byte{0x7f}), "")
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

func TestConnectTCP_RejectsObviouslyBadTarget(t *testing.T) {
	t.Parallel()

	// Past url.Parse but grpc.NewClient should reject the resolved
	// address (NUL bytes in the host segment).
	_, _, err := pkg.ConnectTCP("http://"+strings.Repeat("\x00", 8)+":5500", "")
	if err == nil {
		t.Fatal("expected error for invalid tcp target, got nil")
	}
}
