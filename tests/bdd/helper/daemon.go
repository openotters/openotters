// Package helper spins up an isolated `ottersd` for one BDD test run
// and exposes a single `Run(args...)` method that shells out to the
// `otters` CLI pointed at that daemon's socket.
//
// Each test gets its own tmpdir-rooted daemon: separate Unix socket,
// separate $HOME (so on-disk state under $HOME/.otters/ doesn't bleed
// between tests), and the TCP listener disabled to avoid port
// collisions when multiple executor suites run in parallel.
package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Daemon owns a running ottersd subprocess for the lifetime of one
// test. Use Run() to invoke the otters CLI against it.
type Daemon struct {
	Executor   string
	SocketPath string
	HomeDir    string

	cmd *exec.Cmd
	t   *testing.T
}

// CommandResult is what Run() returns: the captured stdout, stderr,
// and exit code from one `otters …` invocation.
type CommandResult struct {
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
}

// StartDaemon spawns ottersd configured for the given executor backend,
// blocks until it answers a health probe, and registers cleanup so the
// subprocess dies when the test ends. The returned Daemon is safe to
// share across all scenarios in one suite — Run() does not mutate state.
//
// Hard-fails (t.Fatal) when ottersd or otters aren't on PATH. These are
// end-to-end tests against the real CLI surface; a missing binary is
// the user's signal to run `task install` first.
func StartDaemon(t *testing.T, executor string) *Daemon {
	t.Helper()

	for _, bin := range []string{"ottersd", "otters"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s not on PATH — run `task install` before `task test:bdd`", bin)
		}
	}

	home := t.TempDir()
	socket := filepath.Join(home, "otters.sock")

	d := &Daemon{
		Executor:   executor,
		SocketPath: socket,
		HomeDir:    home,
		t:          t,
	}

	d.cmd = exec.CommandContext(t.Context(),
		"ottersd", "serve",
		"--socket-path", socket,
		"--no-http",
		"--executor", executor,
		// `:memory:` doesn't survive the daemon's multi-connection
		// pool — each connection gets its own private database, so
		// migrations run on one connection but tables are missing on
		// the next. Real file in the tmpdir sidesteps it; t.TempDir()
		// cleans up after the test.
		"--sqlite-path", filepath.Join(home, "state.db"),
	)
	env := append(os.Environ(), "HOME="+home)
	if executor == "docker" {
		// ottersd's docker client hardcodes /var/run/docker.sock when
		// DOCKER_HOST is unset — fine on Linux, broken on macOS/Colima
		// where the socket lives elsewhere. Mirror whatever the
		// `docker` CLI is using.
		if host := DockerHost(); host != "" {
			env = append(env, "DOCKER_HOST="+host)
		}
	}
	d.cmd.Env = env
	d.cmd.Stdout = newPrefixWriter(t, "[ottersd:"+executor+":out] ")
	d.cmd.Stderr = newPrefixWriter(t, "[ottersd:"+executor+":err] ")

	if err := d.cmd.Start(); err != nil {
		t.Fatalf("starting ottersd: %v", err)
	}

	t.Cleanup(d.stop)

	if err := d.waitReady(15 * time.Second); err != nil {
		t.Fatalf("daemon never became ready: %v", err)
	}

	return d
}

// Run invokes `otters --socket <path> <args>...` and returns its output
// and exit code. Non-zero exits do NOT fail the test — scenarios assert
// on the result themselves.
func (d *Daemon) Run(args ...string) CommandResult {
	d.t.Helper()

	full := append([]string{"--socket", d.SocketPath}, args...)
	cmd := exec.CommandContext(d.t.Context(), "otters", full...)
	cmd.Env = append(os.Environ(), "HOME="+d.HomeDir)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if asExit(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			d.t.Fatalf("running otters %v: %v", args, err)
		}
	}

	return CommandResult{
		Args:     full,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
}

func (d *Daemon) stop() {
	if d.cmd == nil || d.cmd.Process == nil {
		return
	}
	_ = d.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- d.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = d.cmd.Process.Kill()
	}
}

// waitReady polls `otters info` until it exits 0 or the deadline hits.
// Using the real CLI for the probe means we test the full client path
// alongside the daemon — a misconfigured socket fails here, not later.
func (d *Daemon) waitReady(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout after %s", timeout)
		default:
		}

		// G204 false positive: otters is a literal hardcoded value;
		// d.SocketPath is t.TempDir-derived (test-scoped, no operator
		// input). Test code only.
		//
		//nolint:gosec // hardcoded bin; SocketPath is t.TempDir, not user input
		probe := exec.CommandContext(d.t.Context(),
			"otters", "--socket", d.SocketPath, "info")
		probe.Env = append(os.Environ(), "HOME="+d.HomeDir)
		if err := probe.Run(); err == nil {
			return nil
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// asExit is `errors.As(err, &target)` shrunk to one line — kept here so
// callers don't grow an `import "errors"` for a single use site.
func asExit(err error, target **exec.ExitError) bool {
	e := &exec.ExitError{}
	if errors.As(err, &e) {
		*target = e
		return true
	}
	return false
}
