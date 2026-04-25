package commands

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// Info asks the daemon for its runtime coordinates — listen paths,
// data dirs, build version, running-agent counts — and prints them
// in a two-column layout. Read-only, cheap, and the canonical way to
// answer "is the daemon alive? which socket? which registry?".
type Info struct{}

func (i *Info) Run(ctx context.Context, _ *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.GetInfo(ctx, &daemonv1.GetInfoRequest{})
	if err != nil {
		return fmt.Errorf("fetching daemon info: %w", unwrapRPC(err))
	}

	rows := [][2]string{
		{"Version", nonEmpty(resp.GetVersion(), "(unknown)")},
		{"Commit", nonEmpty(resp.GetCommit(), "(unknown)")},
		{"Build date", nonEmpty(resp.GetBuildDate(), "(unknown)")},
		{"Socket", resp.GetSocketPath()},
		{"Registry", resp.GetRegistryAddr()},
		{"Data dir", resp.GetDataDir()},
		{"Agents dir", resp.GetAgentsDir()},
		{"Log dir", resp.GetLogDir()},
		{"Runtime", nonEmpty(resp.GetRuntimePath(), "(pulled from OCI)")},
		{"Providers", fmt.Sprintf("%d", resp.GetProviders())},
		{"Agents", fmt.Sprintf("%d running / %d total",
			resp.GetAgentsRunning(), resp.GetAgentsTotal())},
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(w, "%s:\t%s\n", r[0], r[1])
	}

	return w.Flush()
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}

	return s
}
