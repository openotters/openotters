package steps

import (
	"context"

	"github.com/cucumber/godog"

	"github.com/openotters/openotters/tests/bdd/helper"
)

// State is the per-scenario context shared across step files. The
// Daemon pointer is shared across the suite (one daemon per executor);
// Last is reset by godog's Before hook so scenarios don't leak the
// previous command's output into the next one.
type State struct {
	Daemon *helper.Daemon
	Last   helper.CommandResult
}

func newState(daemon *helper.Daemon) *State {
	return &State{Daemon: daemon}
}

// Reset clears per-scenario state. Hooked from RegisterCommon's Before.
func (s *State) Reset() { s.Last = helper.CommandResult{} }

// hookReset registers a Before hook that wipes Last between scenarios.
// Called by RegisterCommon so every step file gets the guarantee
// without each one having to add its own Before.
func hookReset(sc *godog.ScenarioContext, s *State) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.Reset()
		return ctx, nil
	})
}
