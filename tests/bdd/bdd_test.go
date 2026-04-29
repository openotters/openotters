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
// wire shape a user gets. Each scenario isolates its state via a
// per-suite tmp socket + tmp data directory, set up in
// InitializeTestSuite.
//
// The skeleton below intentionally leaves step bodies as
// godog.ErrPending so the very first `task test:bdd` reports each
// step as "pending" rather than silently passing. Wire them up as
// features are implemented.
package bdd_test

import (
	"flag"
	"os"
	"testing"

	"github.com/cucumber/godog"
	"github.com/cucumber/godog/colors"
)

//nolint:gochecknoglobals // godog convention: parsed in TestMain
var opts = godog.Options{
	Output: colors.Colored(os.Stdout),
	Format: "pretty",
	Paths:  []string{"features"},
}

//nolint:gochecknoinits // godog requires CLI flag bindings registered before TestMain runs
func init() {
	godog.BindCommandLineFlags("godog.", &opts)
}

func TestMain(m *testing.M) {
	flag.Parse()

	status := godog.TestSuite{
		Name:                 "openotters",
		TestSuiteInitializer: InitializeTestSuite,
		ScenarioInitializer:  InitializeScenario,
		Options:              &opts,
	}.Run()

	if st := m.Run(); st > status {
		status = st
	}

	os.Exit(status)
}

// InitializeTestSuite is called once per `go test` invocation. Use it
// to spin up shared infrastructure: a clean otters daemon on a
// per-suite unix socket, a scratch ~/.otters/ data dir, etc.
//
// TODO: spawn `ottersd serve --socket-path <tmp>` here and tear it
// down in AfterSuite.
func InitializeTestSuite(_ *godog.TestSuiteContext) {
}

// InitializeScenario binds Gherkin step phrases to Go functions. The
// godog runner matches "Given X" / "When Y" / "Then Z" by regex.
//
// Steps below intentionally return godog.ErrPending — fill them in
// as features land.
func InitializeScenario(ctx *godog.ScenarioContext) {
	ctx.Step(`^the otters daemon is running$`,
		theDaemonIsRunning)
	ctx.Step(`^a base agent image is published at "([^"]+)" with the Agentfile:$`,
		aBaseAgentImageIsPublished)
	ctx.Step(`^I run an Agentfile named "([^"]+)" with the contents:$`,
		iRunAnAgentfile)
	ctx.Step(`^the agent "([^"]+)" should be running$`,
		theAgentShouldBeRunning)
	ctx.Step(`^the agent's runtime should be "([^"]+)"$`,
		theAgentsRuntimeShouldBe)
	ctx.Step(`^the agent's model should be "([^"]+)"$`,
		theAgentsModelShouldBe)
}

// --- step bodies (skeleton) -------------------------------------

func theDaemonIsRunning() error {
	return godog.ErrPending
}

func aBaseAgentImageIsPublished(_ string, _ *godog.DocString) error {
	return godog.ErrPending
}

func iRunAnAgentfile(_ string, _ *godog.DocString) error {
	return godog.ErrPending
}

func theAgentShouldBeRunning(_ string) error {
	return godog.ErrPending
}

func theAgentsRuntimeShouldBe(_ string) error {
	return godog.ErrPending
}

func theAgentsModelShouldBe(_ string) error {
	return godog.ErrPending
}
