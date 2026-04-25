package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// AgentInspect prints the full record of a running agent — status,
// image ref, model, listen addr, creation time. Complements `otters
// ps` (which is the tabular overview) with a detailed per-agent view.
type AgentInspect struct {
	Ref string `arg:"" name:"agent" help:"Agent ID (prefix-matched) or name"`
}

func (a *AgentInspect) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.ListAgents(ctx, &daemonv1.ListAgentsRequest{})
	if err != nil {
		return fmt.Errorf("listing agents: %w", unwrapRPC(err))
	}

	match, err := resolveAgent(resp.GetAgents(), a.Ref)
	if err != nil {
		return err
	}

	p := common.Printer()
	_, _ = p.Printf("id:      %s\n", match.GetId())
	_, _ = p.Printf("name:    %s\n", match.GetName())
	_, _ = p.Printf("image:   %s\n", match.GetImage())
	_, _ = p.Printf("model:   %s\n", match.GetModel())
	_, _ = p.Printf("status:  %s\n", match.GetStatus())
	_, _ = p.Printf("addr:    %s\n", match.GetAddr())
	_, _ = p.Printf("created: %s\n", time.Unix(match.GetCreatedAt(), 0).Format(time.RFC3339))

	if len(match.GetMounts()) > 0 {
		_, _ = p.Println("mounts:")

		for _, m := range match.GetMounts() {
			desc := ""
			if m.GetDescription() != "" {
				desc = " — " + m.GetDescription()
			}

			_, _ = p.Printf("  %s -> %s%s\n", m.GetTarget(), m.GetHost(), desc)
		}
	}

	return nil
}

// resolveAgent matches ref against full id, name, or id prefix — same
// rules the daemon's internal resolver uses. Duplicates the logic
// client-side so `otters agent inspect` doesn't need a new RPC; if it
// ever grows heavier fields, promote to a dedicated InspectAgent RPC.
func resolveAgent(agents []*daemonv1.AgentInfo, ref string) (*daemonv1.AgentInfo, error) {
	if ref == "" {
		return nil, fmt.Errorf("agent ref is required")
	}

	for _, a := range agents {
		if a.GetId() == ref || a.GetName() == ref {
			return a, nil
		}
	}

	for _, a := range agents {
		if strings.HasPrefix(a.GetId(), ref) {
			return a, nil
		}
	}

	return nil, fmt.Errorf("agent %q not found", ref)
}
