package commands

import (
	"context"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// Links prints the directed graph for one agent: who it can call
// (outbound) and who can call it (inbound). Useful for sanity-
// checking the model's view before chatting with it.
type Links struct {
	Ref string `arg:"" name:"agent" help:"Agent to list links for"`
}

func (l *Links) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.ListAgentLinks(ctx, &daemonv1.ListAgentLinksRequest{Ref: l.Ref})
	if err != nil {
		return unwrapRPC(err)
	}

	p := common.Printer()

	_, _ = p.Printf("Outbound (%s can call):\n", l.Ref)
	if len(resp.GetOutbound()) == 0 {
		_, _ = p.Printf("  (none)\n")
	} else {
		for _, a := range resp.GetOutbound() {
			_, _ = p.Printf("  %-30s %-30s %-12s %s\n",
				a.GetName(), a.GetModel(), a.GetStatus(), a.GetDescription())
		}
	}

	_, _ = p.Printf("\nInbound (can call %s):\n", l.Ref)
	if len(resp.GetInbound()) == 0 {
		_, _ = p.Printf("  (none)\n")
	} else {
		for _, a := range resp.GetInbound() {
			_, _ = p.Printf("  %-30s %-30s %-12s %s\n",
				a.GetName(), a.GetModel(), a.GetStatus(), a.GetDescription())
		}
	}

	return nil
}
