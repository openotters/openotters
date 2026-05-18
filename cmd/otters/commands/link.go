// per-target loop on purpose: they're symmetric operator commands
// and the parallel structure is the readability win. Refactoring
// the body out would force callers into a tagged dispatch harder
// to follow than two parallel files.
//
//nolint:dupl // Link and Unlink share the same kong arg shape +
package commands

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// Link establishes one or more directed agent → target edges. The
// daemon re-issues the source's JWT (with the updated Links claim)
// and auto-restarts the source if it's currently running, so the
// new link takes effect immediately.
//
// `otters link orchestrator worker-a worker-b` is equivalent to
// running the command twice with one target each.
type Link struct {
	Source  string   `arg:"" name:"source" help:"Source agent (the one that gains call permission)"`
	Targets []string `arg:"" name:"target" help:"One or more targets the source can call"`
}

func (l *Link) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	if len(l.Targets) == 0 {
		return errors.New("at least one target is required")
	}

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	p := common.Printer()
	var errs []error

	for _, target := range l.Targets {
		resp, linkErr := c.LinkAgents(ctx, &daemonv1.LinkAgentsRequest{
			SourceRef: l.Source,
			TargetRef: target,
		})
		if linkErr != nil {
			clean := unwrapRPC(linkErr)
			fmt.Fprintf(os.Stderr, "link %s → %s: %v\n", l.Source, target, clean)
			errs = append(errs, clean)
			continue
		}
		if resp.GetRestarted() {
			_, _ = p.Printf("linked %s → %s (source restarted)\n", l.Source, target)
		} else {
			_, _ = p.Printf("linked %s → %s\n", l.Source, target)
		}
	}

	return errors.Join(errs...)
}
