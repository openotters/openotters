package steps

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cucumber/godog"
)

// RegisterInfo binds step phrases for `otters info`-shaped assertions.
// These read from the daemon helper to verify the daemon's *own*
// reported state lines up with how the test brought it up.
//
//	Then the output reports the active executor
//	And  the output reports the daemon's socket
//	And  the output reports both CLI and daemon sections
func RegisterInfo(sc *godog.ScenarioContext, s *State) {
	sc.Step(`^the output reports the active executor$`,
		matchKeyValue(s, "Executor", func() string { return s.Daemon.Executor }))

	sc.Step(`^the output reports the daemon's socket$`,
		matchKeyValue(s, "Socket", func() string { return s.Daemon.SocketPath }))

	sc.Step(`^the output reports both CLI and daemon sections$`, func() error {
		// `otters info` opens with two unindented headers; if either
		// is missing the format has drifted and downstream assertions
		// based on the section labels won't catch it.
		for _, header := range []string{"CLI", "Daemon"} {
			matched := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(header) + `\s*$`)
			if !matched.MatchString(s.Last.Stdout) {
				return fmt.Errorf("missing %q section header\nstdout:\n%s",
					header, s.Last.Stdout)
			}
		}
		return nil
	})
}

// matchKeyValue returns a step func that asserts `<key>:` appears in
// the output AND its value (trimmed) equals expected(). The value
// supplier is a closure so steps can reference the daemon helper, which
// is constructed before scenarios run.
//
// The line shape produced by `otters info` is:
//
//	Executor:    docker
//
// so the matcher splits on the colon and trims surrounding whitespace.
func matchKeyValue(s *State, key string, expected func() string) func() error {
	pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(.*)$`)
	return func() error {
		want := expected()
		match := pattern.FindStringSubmatch(s.Last.Stdout)
		if match == nil {
			return fmt.Errorf("no %q line in stdout\nstdout:\n%s", key, s.Last.Stdout)
		}
		got := strings.TrimSpace(match[1])
		if got != want {
			return fmt.Errorf("%s = %q, want %q", key, got, want)
		}
		return nil
	}
}
