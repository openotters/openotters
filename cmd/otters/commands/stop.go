//nolint:dupl // mirrors start.go by design — see comment in start.go
package commands

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

type Stop struct {
	Refs []string `arg:"" name:"agent" help:"Agent ID or name (one or more)"`
}

func (s *Stop) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
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
		if _, stopErr := c.StopAgent(ctx, &daemonv1.StopAgentRequest{Ref: ref}); stopErr != nil {
			clean := unwrapRPC(stopErr)
			fmt.Fprintf(os.Stderr, "stop %s: %v\n", ref, clean)
			errs = append(errs, clean)

			continue
		}

		_, _ = p.Printf("stopped %s\n", ref)
	}

	return errors.Join(errs...)
}
