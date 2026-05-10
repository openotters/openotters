package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// parseMount turns a `-v HOST:TARGET[:DESC][:ro|:rw]` CLI spec into
// the protobuf Mount the daemon consumes. The client is responsible
// for resolving `~`/`$PWD`/relative host paths to absolute form —
// daemon-side validation assumes absolute input.
//
// Mode suffix:
//
//   - `:ro` → mount is read-only (docker bind ReadOnly = true; on
//     system the model is told via MOUNTS.md but no kernel-level
//     enforcement)
//   - `:rw` → explicit read-write (default; accepted for parity)
//   - omitted → read-write
//
// The mode is only recognised as the *last* colon-separated segment
// matching exactly "ro" or "rw" — descriptions like "rwlock test"
// stay intact.
func parseMount(spec string) (*daemonv1.Mount, error) {
	if spec == "" {
		return nil, fmt.Errorf("empty -v spec")
	}

	// Pop a trailing :ro / :rw if present.
	readOnly := false
	if i := strings.LastIndexByte(spec, ':'); i > 0 {
		switch spec[i+1:] {
		case "ro":
			readOnly = true
			spec = spec[:i]
		case "rw":
			spec = spec[:i]
		}
	}

	host, rest, ok := strings.Cut(spec, ":")
	if !ok || rest == "" {
		return nil, fmt.Errorf("mount %q must be HOST:TARGET[:DESC][:ro|:rw]", spec)
	}

	target, desc, _ := strings.Cut(rest, ":")

	host, err := resolveHost(host)
	if err != nil {
		return nil, err
	}

	// Accept absolute targets (`/path`) or agent-root relative ones
	// (`./path`, `path`). Relatives are resolved against the agent's
	// chroot root by the daemon — which is also where each BIN tool
	// starts with CWD, so `--socket ./otters.sock` on a tool call
	// and `-v HOST:./otters.sock` on the launch line land at the
	// same spot.
	target = filepath.Clean(target)
	if target == "" || target == "." {
		return nil, fmt.Errorf("mount target is empty")
	}

	return &daemonv1.Mount{
		Host:        host,
		Target:      target,
		Description: desc,
		ReadOnly:    readOnly,
	}, nil
}

// resolveHost expands leading `~`, relative paths, and `$VAR`/`${VAR}`
// references to an absolute path, then stats the result so we fail
// fast on the client before an RPC round-trip. The daemon does its
// own os.Stat but a friendlier error lives closer to the user.
func resolveHost(host string) (string, error) {
	host = os.ExpandEnv(host)

	if strings.HasPrefix(host, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving ~: %w", err)
		}

		host = filepath.Join(home, strings.TrimPrefix(host, "~"))
	}

	abs, err := filepath.Abs(host)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", host, err)
	}

	if _, statErr := os.Stat(abs); statErr != nil {
		return "", fmt.Errorf("mount host %s: %w", abs, statErr)
	}

	return abs, nil
}
