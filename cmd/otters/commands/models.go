package commands

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// ModelsLs lists every model reachable through the daemon's
// configured providers. Providers declared without an explicit
// `models:` list show up as a single `<provider>/*` row — they
// accept any model name the upstream API supports.
type ModelsLs struct {
	Quiet    bool   `short:"q" help:"Only display model refs (useful for piping)" default:"false"`
	Provider string `help:"Filter to a single provider" default:""`
}

func (m *ModelsLs) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.ListModels(ctx, &daemonv1.ListModelsRequest{})
	if err != nil {
		return fmt.Errorf("listing models: %w", unwrapRPC(err))
	}

	models := resp.GetModels()
	if m.Provider != "" {
		filtered := models[:0]

		for _, row := range models {
			if row.GetProvider() == m.Provider {
				filtered = append(filtered, row)
			}
		}

		models = filtered
	}

	if m.Quiet {
		for _, row := range models {
			fmt.Fprintln(os.Stdout, row.GetRef())
		}

		return nil
	}

	if len(models) == 0 {
		_, _ = common.Printer().Println("no models configured")

		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "REF\tCONTEXT\tIN $/1M\tOUT $/1M\tREASONS\tDISPLAY NAME")

	for _, row := range models {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			row.GetRef(),
			humanTokens(row.GetContextWindow()),
			humanCost(row.GetCostInputPer_1M()),
			humanCost(row.GetCostOutputPer_1M()),
			yesOrDash(row.GetCanReason()),
			fallback(row.GetDisplayName(), row.GetName()),
		)
	}

	return w.Flush()
}

// humanTokens compacts a context-window count into a readable
// "128K" / "1M" shape; 0 renders as "-" so we don't leak Catwalk
// gaps as "0".
func humanTokens(n int64) string {
	switch {
	case n == 0:
		return "-"
	case n >= 1_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// humanCost renders a per-1M-token price in dollars with two
// decimals, hiding a zero as "-" so the column isn't noisy for
// models whose pricing isn't in Catwalk.
func humanCost(c float64) string {
	if c == 0 {
		return "-"
	}

	return fmt.Sprintf("$%.2f", c)
}

func yesOrDash(b bool) string {
	if b {
		return "yes"
	}

	return "-"
}

func fallback(primary, alt string) string {
	if primary != "" {
		return primary
	}

	return alt
}
