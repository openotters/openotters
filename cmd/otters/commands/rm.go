package commands

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

type Rm struct {
	Refs  []string `arg:"" name:"agent" help:"Agent ID or name (one or more)"`
	Force bool     `short:"f" help:"Force remove (stop each first if running)" default:"false"`
}

func (r *Rm) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	if len(r.Refs) == 0 {
		return errors.New("at least one agent is required")
	}

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	p := common.Printer()

	var errs []error

	for _, ref := range r.Refs {
		if r.Force {
			_, _ = c.StopAgent(ctx, &daemonv1.StopAgentRequest{Ref: ref})
		}

		if _, rmErr := c.RemoveAgent(ctx, &daemonv1.RemoveAgentRequest{Ref: ref}); rmErr != nil {
			clean := unwrapRPC(rmErr)
			fmt.Fprintf(os.Stderr, "rm %s: %v\n", ref, clean)
			errs = append(errs, clean)

			continue
		}

		_, _ = p.Printf("removed %s\n", ref)
	}

	return errors.Join(errs...)
}
