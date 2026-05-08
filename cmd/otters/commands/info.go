package commands

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// Info prints the locally-installed CLI build alongside the daemon's
// runtime coordinates. The two sections sit side by side so users can
// see at a glance whether their CLI matches the daemon they're talking
// to — useful after a `brew upgrade` or a partial replace.
type Info struct{}

func (i *Info) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.GetInfo(ctx, &daemonv1.GetInfoRequest{})
	if err != nil {
		return fmt.Errorf("fetching daemon info: %w", unwrapRPC(err))
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	v := common.Version
	fmt.Fprintln(w, "CLI")
	for _, r := range [][2]string{
		{"  Version", nonEmpty(v.Version(), "(unknown)")},
		{"  Commit", nonEmpty(v.Commit(), "(unknown)")},
		{"  Build date", nonEmpty(v.Date(), "(unknown)")},
		{"  Build source", nonEmpty(v.BuildSource(), "(unknown)")},
	} {
		fmt.Fprintf(w, "%s:\t%s\n", r[0], r[1])
	}

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Daemon")
	for _, r := range [][2]string{
		{"  Version", nonEmpty(resp.GetVersion(), "(unknown)")},
		{"  Commit", nonEmpty(resp.GetCommit(), "(unknown)")},
		{"  Build date", nonEmpty(resp.GetBuildDate(), "(unknown)")},
		{"  Socket", resp.GetSocketPath()},
		{"  Executor", nonEmpty(resp.GetExecutor(), "system")},
		{"  Registry", resp.GetRegistryAddr()},
		{"  Data dir", resp.GetDataDir()},
		{"  Agents dir", resp.GetAgentsDir()},
		{"  Log dir", resp.GetLogDir()},
		{"  Runtime", nonEmpty(resp.GetRuntimePath(), "(pulled from OCI)")},
		{"  Providers", fmt.Sprintf("%d", resp.GetProviders())},
		{"  Agents", fmt.Sprintf("%d running / %d total",
			resp.GetAgentsRunning(), resp.GetAgentsTotal())},
	} {
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
