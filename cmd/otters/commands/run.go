package commands

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// Run launches an agent from either a registry ref or a local
// Agentfile. Three positional shapes:
//
//   - "-"                       → build from stdin, then run
//   - ./path or ./dir           → build from disk, then run
//   - any other string          → treat as an image ref and run
//
// Stdin and disk paths go through resolveBuildSource (shared with
// `otters image build`) so both commands honour the same contract.
type Run struct {
	Ref     string   `arg:"" help:"Image ref, Agentfile path / build context, or '-' to read the Agentfile from stdin"`
	Name    string   `help:"Instance name (auto-generated if empty)" optional:""`
	Model   string   `help:"Override the image's declared MODEL directive" optional:""`
	Runtime string   `help:"Override the image's declared RUNTIME reference" optional:""`
	Mounts  []string `short:"v" name:"mount" help:"Bind host path into agent: HOST:TARGET[:DESC][:ro|:rw]. HOST is resolved client-side (~, relative paths); must exist on the daemon host. Trailing :ro makes the mount read-only (docker enforces; system surfaces it to the model)."`
	Envs    []string `short:"e" name:"env" help:"Set or override an env var on the agent: KEY=VALUE. Repeatable. Wins over Agentfile-declared ENV with the same key. Reserved keys (PATH, *_API_KEY, OTTERS_AGENT_ROOT, …) are rejected."`
	Labels  []string `name:"label" help:"Attach a key=value label. Repeatable. Reserved keys live under io.openotters.* — see the daemon proto for the standard set (origin, etc.). Filterable via 'otters ps --label'."`
}

func (r *Run) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	ref := r.Ref

	// Stdin and on-disk paths both route through the build pipeline.
	// "-" is stdin (the os.Stat trick below doesn't catch it so we
	// check explicitly); anything else that stat's as a file or dir
	// goes through resolveBuildSource too.
	if _, statErr := os.Stat(ref); statErr == nil || ref == "-" {
		abs, cleanup, resolveErr := resolveBuildSource(ref, "")
		if resolveErr != nil {
			return resolveErr
		}
		defer cleanup()

		build, buildErr := c.BuildAgent(ctx, &daemonv1.BuildAgentRequest{AgentfilePath: abs})
		if buildErr != nil {
			return fmt.Errorf("building: %w", unwrapRPC(buildErr))
		}

		// BuildAgent returns the pushed tags; first tag is the
		// friendly name, use that so CreateAgent's short-ref resolver
		// picks it up without leaking the embedded registry address.
		tags := build.GetTags()
		if len(tags) == 0 {
			return fmt.Errorf("build produced no tags for %s", abs)
		}

		ref = tags[0]
	}

	mounts := make([]*daemonv1.Mount, 0, len(r.Mounts))
	for _, spec := range r.Mounts {
		m, mErr := parseMount(spec)
		if mErr != nil {
			return mErr
		}

		mounts = append(mounts, m)
	}

	envs := make([]*daemonv1.EnvOverride, 0, len(r.Envs))
	for _, raw := range r.Envs {
		eq := strings.IndexByte(raw, '=')
		if eq <= 0 {
			return fmt.Errorf("invalid env %q: expected KEY=VALUE", raw)
		}
		envs = append(envs, &daemonv1.EnvOverride{
			Key:   raw[:eq],
			Value: raw[eq+1:],
		})
	}

	labels, err := parseKVPairs(r.Labels)
	if err != nil {
		return fmt.Errorf("--label: %w", err)
	}

	resp, err := c.CreateAgent(ctx, &daemonv1.CreateAgentRequest{
		Name:    r.Name,
		Ref:     ref,
		Model:   r.Model,
		Runtime: r.Runtime,
		Mounts:  mounts,
		Envs:    envs,
		Labels:  labels,
	})
	if err != nil {
		return fmt.Errorf("creating agent: %w", unwrapRPC(err))
	}

	// CreateAgent returns while the pool is still spinning the runtime
	// subprocess up; poll briefly so the user sees the final state
	// (running / init_error) rather than the momentary "created".
	final := waitForTerminalStatus(ctx, c, resp.GetId(), resp.GetStatus(), resp.GetName())

	p := common.Printer()
	_, _ = p.Printf("created %s\n", resp.GetName())
	_, _ = p.Printf("  id:     %s\n", resp.GetId())
	_, _ = p.Printf("  status: %s\n", final.GetStatus())

	if addr := final.GetAddr(); addr != "" {
		_, _ = p.Printf("  addr:   %s\n", addr)
	}

	return nil
}

// waitForTerminalStatus polls ListAgents until the named agent
// reaches a status that won't change on its own (running, stopped,
// init_error, pull_error, model_error) or until the budget runs out. Returns the
// most recent snapshot; on any RPC failure or cancellation we fall
// back to a synthetic AgentInfo so the caller can still surface the
// initial create response.
func waitForTerminalStatus(
	ctx context.Context, c daemonv1.RuntimeClient, id, initialStatus, name string,
) *daemonv1.AgentInfo {
	fallback := &daemonv1.AgentInfo{Id: id, Name: name, Status: initialStatus}

	deadline := time.Now().Add(2 * time.Second)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		resp, err := c.ListAgents(ctx, &daemonv1.ListAgentsRequest{})
		if err != nil {
			return fallback
		}

		for _, a := range resp.GetAgents() {
			if a.GetId() != id {
				continue
			}

			if isTerminalStatus(a.GetStatus()) {
				return a
			}

			fallback = a

			break
		}

		if time.Now().After(deadline) {
			return fallback
		}

		select {
		case <-ctx.Done():
			return fallback
		case <-ticker.C:
		}
	}
}

func isTerminalStatus(s string) bool {
	switch s {
	case "running", "stopped", "init_error", "pull_error", "model_error":
		return true
	}

	return false
}
