package steps

import (
	"fmt"

	"github.com/cucumber/godog"
)

// RegisterJobs binds step phrases used by features/jobs.feature.
//
// At present these are negative-path / wire-surface assertions only
// — `otters jobs run <agent> --bin echo` against a live agent is
// covered by the asyncjobs / executor unit tests, since BDD here
// can't yet stand up a real agent without the in-test build
// pipeline (see the inheritance @pending scenario).
//
// The "exit code is not N" matcher here is a minor superset of
// common.go's exact match — kept here rather than promoting it so
// area-specific steps can add their own polarity matchers without
// growing the common surface.
//
//	When  I run "otters jobs …"
//	Then  the exit code is not 0
func RegisterJobs(sc *godog.ScenarioContext, s *State) {
	sc.Step(`^the exit code is not (\d+)$`, func(forbidden int) error {
		if s.Last.ExitCode == forbidden {
			return fmt.Errorf("exit code = %d, expected anything else\nstderr:\n%s",
				s.Last.ExitCode, s.Last.Stderr)
		}
		return nil
	})
}
