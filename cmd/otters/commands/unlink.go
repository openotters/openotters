// parallel structure preferred over a tagged-dispatch refactor.
//
//nolint:dupl // See link.go for the rationale; symmetric command,
package commands

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// Unlink removes one or more directed agent → target edges. Like
// `otters link`, the daemon auto-restarts the source so the
// shorter JWT Links claim takes effect immediately.
type Unlink struct {
	Source  string   `arg:"" name:"source" help:"Source agent"`
	Targets []string `arg:"" name:"target" help:"One or more targets to revoke"`
}

func (u *Unlink) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	if len(u.Targets) == 0 {
		return errors.New("at least one target is required")
	}

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	p := common.Printer()
	var errs []error

	for _, target := range u.Targets {
		resp, unlinkErr := c.UnlinkAgents(ctx, &daemonv1.UnlinkAgentsRequest{
			SourceRef: u.Source,
			TargetRef: target,
		})
		if unlinkErr != nil {
			clean := unwrapRPC(unlinkErr)
			fmt.Fprintf(os.Stderr, "unlink %s → %s: %v\n", u.Source, target, clean)
			errs = append(errs, clean)
			continue
		}
		if resp.GetRestarted() {
			_, _ = p.Printf("unlinked %s → %s (source restarted)\n", u.Source, target)
		} else {
			_, _ = p.Printf("unlinked %s → %s\n", u.Source, target)
		}
	}

	return errors.Join(errs...)
}
