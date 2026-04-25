package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

type Logs struct {
	Ref   string `arg:"" name:"agent" help:"Agent ID or name"`
	Lines int64  `short:"n" help:"Return only the last N lines (0 = no cap)" default:"0"`
	Bytes int64  `short:"c" help:"Return only the last N bytes (0 = no cap); ignored when --lines is set" default:"0"`
	Path  bool   `help:"Print the log file path and exit" default:"false"`
}

func (l *Logs) Run(ctx context.Context, _ *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.GetAgentLogs(ctx, &daemonv1.GetAgentLogsRequest{
		Ref:       l.Ref,
		TailBytes: l.Bytes,
		TailLines: l.Lines,
	})
	if err != nil {
		return fmt.Errorf("fetching logs: %w", unwrapRPC(err))
	}

	if l.Path {
		fmt.Fprintln(os.Stdout, resp.GetPath())

		return nil
	}

	_, _ = os.Stdout.Write(resp.GetContent())

	return nil
}
