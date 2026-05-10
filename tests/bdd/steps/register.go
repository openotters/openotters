// Package steps holds godog step definitions, grouped by area.
//
// Adding a new CLI surface is two lines of plumbing:
//
//  1. Drop a `<area>.go` in this package with a `Register<Area>` func
//     that takes (*godog.ScenarioContext, *State).
//  2. Call it from RegisterAll below.
//
// Every test pulls steps from this package via RegisterAll; individual
// area Register funcs are exported only so they can be composed
// differently in the future (e.g. one suite per area).
package steps

import (
	"github.com/cucumber/godog"

	"github.com/openotters/openotters/tests/bdd/helper"
)

// RegisterAll wires every step area onto the scenario context. Used
// by the top-level TestBDD entry point so a single go test run picks
// up every scenario across every executor.
func RegisterAll(sc *godog.ScenarioContext, daemon *helper.Daemon) {
	state := newState(daemon)

	RegisterCommon(sc, state)
	RegisterInfo(sc, state)
	RegisterJobs(sc, state)
}
