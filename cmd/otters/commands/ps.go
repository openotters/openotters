package commands

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

type Ps struct {
	Quiet   bool `short:"q" help:"Only display agent IDs (useful for piping)" default:"false"`
	Verbose bool `help:"Show extra columns (mounts, etc.)" default:"false"`
}

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

	if p.Quiet {
		for _, a := range resp.GetAgents() {
			fmt.Fprintln(os.Stdout, a.GetId())
		}

		return nil
	}

	if len(resp.Agents) == 0 {
		_, _ = common.Printer().Println("no agents running")

		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)

	if p.Verbose {
		fmt.Fprintln(w, "ID\tNAME\tIMAGE\tMODEL\tSTATUS\tADDR\tCREATED\tMOUNTS")
	} else {
		fmt.Fprintln(w, "ID\tNAME\tIMAGE\tMODEL\tSTATUS\tADDR\tCREATED")
	}

	for _, a := range resp.Agents {
		id := a.Id
		if len(id) > 8 {
			id = id[:8]
		}

		created := time.Unix(a.CreatedAt, 0).Format(time.RFC3339)

		if p.Verbose {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				id, a.Name, a.Image, a.Model, a.Status, a.Addr, created, summarizeMounts(a.GetMounts()))
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				id, a.Name, a.Image, a.Model, a.Status, a.Addr, created)
		}
	}

	return w.Flush()
}

// summarizeMounts renders `target←host,target←host,…` on a single
// line so the tabwriter stays aligned. Empty slices render as "-"
// so the column never collapses to a blank that visually merges
// with the previous one.
func summarizeMounts(mounts []*daemonv1.Mount) string {
	if len(mounts) == 0 {
		return "-"
	}

	parts := make([]string, 0, len(mounts))
	for _, m := range mounts {
		parts = append(parts, fmt.Sprintf("%s←%s", m.GetTarget(), m.GetHost()))
	}

	return strings.Join(parts, ",")
}
