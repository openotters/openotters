package commands

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/merlindorin/go-shared/pkg/cmd"
	daemonv1 "github.com/openotters/cli/api/v1"
)

type Ps struct{}

func (p *Ps) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.ListAgents(ctx, &daemonv1.ListAgentsRequest{})
	if err != nil {
		return err
	}

	if len(resp.Agents) == 0 {
		common.MustLogger().Info("no agents running")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tMODEL\tSTATUS\tCREATED")

	for _, a := range resp.Agents {
		id := a.Id
		if len(id) > 8 {
			id = id[:8]
		}

		created := time.Unix(a.CreatedAt, 0).Format(time.RFC3339)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", id, a.Name, a.Model, a.Status, created)
	}

	return w.Flush()
}
