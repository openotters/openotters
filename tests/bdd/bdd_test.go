// Package bdd runs the Gherkin feature suites under features/ via godog.
//
// The intent is two-fold:
//  1. Living documentation — each .feature file is a behaviour an
//     otters operator should be able to rely on. New work starts
//     with a Scenario; the implementation finishes when it passes.
//  2. Bug capture — when a defect is found, write a Scenario tagged
//     `# @bug` that reproduces it. Once the underlying bug is fixed
//     the scenario starts passing and stays in the suite as a
//     regression guard.
//
// Steps shell out to the locally-installed `otters` and `ottersd`
// binaries (whatever PATH resolves) so the tests exercise the same
// wire shape a user gets. Each executor (system, docker) runs in its
// own t.Run subtest with an isolated daemon (per-suite tmp socket +
// tmp $HOME), so failures don't cross-contaminate and the test name
// tells you which executor surfaced the bug.
//
// Docker subtest is hard-failed (not skipped) when docker is
// unavailable — silent skips hide regressions from CI. Set
// OPENOTTERS_BDD_SKIP=docker to opt out on dev hosts that
// intentionally don't run Colima/Docker.
package bdd_test

import (
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	"github.com/cucumber/godog/colors"

	"github.com/openotters/openotters/tests/bdd/helper"
	"github.com/openotters/openotters/tests/bdd/steps"
)

// executors lists every backend the suite exercises. Order matters:
// system runs first because it has no external dependencies (host
// subprocess only), so a misconfigured BDD setup fails fast on the
// cheaper backend before paying the docker cost.
//
//nolint:gochecknoglobals // configuration constant, not state
var executors = []string{"system", "docker"}

// TestBDD is the single top-level entry point: one t.Run per
// executor, each spinning up an isolated daemon and running every
// scenario under features/ against it. godog's runner is invoked
// programmatically (not via TestMain) so we get a real subtest
// name in -v output and CI failures point at the offending executor
// directly.
func TestBDD(t *testing.T) {
	skip := skipSet(os.Getenv("OPENOTTERS_BDD_SKIP"))

	for _, executor := range executors {
		executor := executor // pin for the closure
		t.Run(executor, func(t *testing.T) {
			if skip[executor] {
				t.Skipf("OPENOTTERS_BDD_SKIP includes %q — opted out", executor)
			}
			if executor == "docker" && !helper.DockerAvailable() {
				// Hard-fail on missing docker. CI must run both
				// executors; dev hosts that intentionally don't have
				// docker should set OPENOTTERS_BDD_SKIP=docker so
				// the opt-out is explicit and visible in the test
				// command, not buried in a green "skip".
				t.Fatalf("docker not available — start it (`colima start` on macOS) " +
					"or set OPENOTTERS_BDD_SKIP=docker to opt out")
			}

			daemon := helper.StartDaemon(t, executor)
			runGodog(t, daemon)
		})
	}
}

// runGodog drives the godog runner against one daemon. Each scenario
// shares the daemon (cheap to set up, expensive to tear down per
// scenario) but resets its scratch state via the steps package's
// Before hook so output from the previous scenario doesn't leak.
//
// Scenario failures bubble back through t.Errorf rather than
// t.Fatalf so the runner reports every failing scenario in one
// pass; the operator gets a full picture of what's broken instead
// of "first failure wins.".
func runGodog(t *testing.T, daemon *helper.Daemon) {
	t.Helper()

	suite := godog.TestSuite{
		Name: "openotters/" + daemon.Executor,
		Options: &godog.Options{
			Output:   colors.Colored(os.Stdout),
			Format:   "pretty",
			Paths:    []string{"features"},
			TestingT: t,
			// Skip scenarios explicitly tagged @pending — they're
			// known-to-fail bug captures or work-in-progress for
			// features whose supporting steps haven't landed yet.
			// Anything else with undefined steps trips Strict and
			// fails the suite — pending stubs MUST be implemented
			// before merge, not silently green.
			Tags:   "~@pending",
			Strict: true,
		},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			steps.RegisterAll(sc, daemon)
		},
	}

	if status := suite.Run(); status != 0 {
		t.Errorf("godog suite exited with status %d", status)
	}
}

// skipSet parses a comma-separated env var into a set keyed on
// trimmed lowercase tokens. Empty input → empty set. Used to honor
// OPENOTTERS_BDD_SKIP=docker (or =system,docker for the cowards).
func skipSet(raw string) map[string]bool {
	out := map[string]bool{}
	if raw == "" {
		return out
	}
	for _, tok := range strings.Split(raw, ",") {
		t := strings.ToLower(strings.TrimSpace(tok))
		if t != "" {
			out[t] = true
		}
	}
	return out
}
