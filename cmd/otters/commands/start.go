// Package-level note: Start and Stop are intentionally parallel
// command shapes — the dupl warning between start.go and stop.go is
// expected and silenced where the duplication appears.

//nolint:dupl // mirrors stop.go by design — see comment above
package commands

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

type Start struct {
	Refs []string `arg:"" name:"agent" help:"Agent ID or name (one or more)"`
}

func (s *Start) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	if len(s.Refs) == 0 {
		return errors.New("at least one agent is required")
	}

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	p := common.Printer()

	var errs []error

	for _, ref := range s.Refs {
		if _, startErr := c.StartAgent(ctx, &daemonv1.StartAgentRequest{Ref: ref}); startErr != nil {
			clean := unwrapRPC(startErr)
			fmt.Fprintf(os.Stderr, "start %s: %v\n", ref, clean)
			errs = append(errs, clean)

			continue
		}

		_, _ = p.Printf("started %s\n", ref)
	}

	return errors.Join(errs...)
}
