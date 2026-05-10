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
