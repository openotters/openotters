package steps

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"
)

// RegisterCommon binds the always-available step phrases — running
// the otters CLI and asserting on its exit code / output. Area-specific
// step files build on top of these.
//
//	Given a fresh daemon
//	When  I run "otters …"
//	Then  the exit code is N
//	And   the output is empty
//	And   the output contains "…"
//	And   the stderr contains "…"
func RegisterCommon(sc *godog.ScenarioContext, s *State) {
	hookReset(sc, s)

	sc.Step(`^a fresh daemon$`, func() error { return nil })

	sc.Step(`^I run "otters ([^"]+)"$`, func(args string) error {
		s.Last = s.Daemon.Run(strings.Fields(args)...)
		return nil
	})

	sc.Step(`^the exit code is (\d+)$`, func(want int) error {
		if s.Last.ExitCode != want {
			return fmt.Errorf("exit code = %d, want %d\nstderr:\n%s",
				s.Last.ExitCode, want, s.Last.Stderr)
		}
		return nil
	})

	sc.Step(`^the output is empty$`, func() error {
		if got := strings.TrimSpace(s.Last.Stdout); got != "" {
			return fmt.Errorf("expected empty stdout, got:\n%s", got)
		}
		return nil
	})

	sc.Step(`^the output contains "([^"]+)"$`, func(want string) error {
		if !strings.Contains(s.Last.Stdout, want) {
			return fmt.Errorf("stdout does not contain %q\nstdout:\n%s",
				want, s.Last.Stdout)
		}
		return nil
	})

	sc.Step(`^the stderr contains "([^"]+)"$`, func(want string) error {
		if !strings.Contains(s.Last.Stderr, want) {
			return fmt.Errorf("stderr does not contain %q\nstderr:\n%s",
				want, s.Last.Stderr)
		}
		return nil
	})
}
