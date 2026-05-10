package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// JobsRun submits an async BIN job against the named agent. Jobs
// are attached to the agent only — relate them to a chat session by
// passing `--label io.openotters.session-id=<id>` (or any other
// labels for grouping / filtering).
//
// Args after the agent/bin are passed as positional varargs,
// separated by `--` from the named flags so dash-prefixed BIN args
// (e.g. `sh -c '…'`) don't collide with the CLI's own flag parser.
//
//	task client:dev -- jobs run my-agent --bin sh \
//	    --label io.openotters.session-id=cli:chat:abc \
//	    --label io.openotters.origin=cli \
//	    -- -c 'echo hi'
type JobsRun struct {
	Agent  string   `arg:"" name:"agent" help:"Agent name or UUID — must already be running"`
	Bin    string   `name:"bin" required:"" help:"BIN name as declared in the agent's Agentfile (e.g. sh, jq, ffmpeg)"`
	Args   []string `arg:"" optional:"" passthrough:"" name:"args" help:"Arguments forwarded to the BIN. Anything after '--' is captured here verbatim — including dash-prefixed flags like sh's -c."`
	Stdin  string   `name:"stdin" help:"stdin payload — literal string, @<file> to read from a file, or - to read from this CLI's stdin"`
	Labels []string `name:"label" help:"Attach a key=value label. Repeatable. Reserved keys live under io.openotters.* — see the daemon proto for the standard set (session-id, origin)."`
}

func (j *JobsRun) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	stdin, err := readStdinFlag(j.Stdin)
	if err != nil {
		return err
	}

	labels, err := parseKVPairs(j.Labels)
	if err != nil {
		return fmt.Errorf("--label: %w", err)
	}

	resp, err := c.SubmitAsyncJob(ctx, &daemonv1.SubmitAsyncJobRequest{
		AgentRef: j.Agent,
		Bin:      j.Bin,
		Args:     j.Args,
		Stdin:    stdin,
		Labels:   labels,
	})
	if err != nil {
		return fmt.Errorf("submitting job: %w", unwrapRPC(err))
	}

	p := common.Printer()
	_, _ = p.Printf("submitted %s\n", resp.GetJobId())
	_, _ = p.Printf("  await:   otters jobs await %s\n", resp.GetJobId())
	_, _ = p.Printf("  inspect: otters jobs inspect %s\n", resp.GetJobId())
	return nil
}

// parseKVPairs converts repeated `--flag key=value` strings into a
// map. Empty input → nil map (cheaper than empty map for the wire).
// First `=` splits; values may contain further `=`. Empty keys and
// duplicates are errors — silent overwrite would mask typos.
func parseKVPairs(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, s := range raw {
		eq := strings.IndexByte(s, '=')
		if eq < 1 {
			return nil, fmt.Errorf("expected key=value, got %q", s)
		}
		k := s[:eq]
		v := s[eq+1:]
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("duplicate key %q", k)
		}
		out[k] = v
	}
	return out, nil
}

// JobsAwait polls GetAsyncJob until the job reaches a terminal status,
// then prints stdout and exits with the BIN's exit code (or 1 for
// error/cancelled/orphaned). Pure CLI-side — no new daemon RPC needed.
type JobsAwait struct {
	JobID    string        `arg:"" name:"job-id" help:"Job ID returned by 'otters jobs run'"`
	Interval time.Duration `name:"interval" default:"500ms" help:"Poll interval"`
	Timeout  time.Duration `name:"timeout" default:"0" help:"Maximum time to wait (0 = no timeout)"`
}

func (j *JobsAwait) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	if j.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, j.Timeout)
		defer cancel()
	}

	tick := time.NewTicker(j.Interval)
	defer tick.Stop()

	for {
		resp, err := c.GetAsyncJob(ctx, &daemonv1.GetAsyncJobRequest{JobId: j.JobID})
		if err != nil {
			return fmt.Errorf("get: %w", unwrapRPC(err))
		}
		job := resp.GetJob()

		if isTerminal(job.GetStatus()) {
			p := common.Printer()
			if job.GetStdout() != "" {
				_, _ = p.Printf("%s", job.GetStdout())
				if !strings.HasSuffix(job.GetStdout(), "\n") {
					_, _ = p.Printf("\n")
				}
			}
			if job.GetStderr() != "" {
				_, _ = fmt.Fprint(os.Stderr, job.GetStderr())
				if !strings.HasSuffix(job.GetStderr(), "\n") {
					_, _ = fmt.Fprintln(os.Stderr)
				}
			}
			switch job.GetStatus() {
			case "done":
				if job.GetExitCode() != 0 {
					os.Exit(int(job.GetExitCode()))
				}
				return nil
			case "error":
				return fmt.Errorf("job %s errored: %s", j.JobID, job.GetError())
			case "cancelled":
				return fmt.Errorf("job %s cancelled", j.JobID)
			case "orphaned":
				return fmt.Errorf("job %s orphaned (daemon restarted before completion)", j.JobID)
			}
			return fmt.Errorf("unexpected terminal status %q", job.GetStatus())
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("await: %w", ctx.Err())
		case <-tick.C:
		}
	}
}

// JobsWatch consumes the daemon's server-streaming WatchAsyncJob.
// Each row update prints one JSON-ish status line; closes cleanly
// when the job reaches a terminal status.
//
// Compared to `await`: `watch` reflects every intermediate status
// flip (pending → running → done) and also surfaces handle changes
// (PID assigned, container ID assigned). `await` is the right
// choice for "block until done"; `watch` is the right choice for
// "see what's happening".
type JobsWatch struct {
	JobID string `arg:"" name:"job-id" help:"Job ID returned by 'otters jobs run'"`
}

func (j *JobsWatch) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	stream, err := c.WatchAsyncJob(ctx, &daemonv1.WatchAsyncJobRequest{JobId: j.JobID})
	if err != nil {
		return fmt.Errorf("watch: %w", unwrapRPC(err))
	}

	p := common.Printer()
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("watch recv: %w", unwrapRPC(err))
		}
		job := resp.GetJob()
		_, _ = p.Printf("%s status=%s handle=%s exit=%d\n",
			job.GetId(), job.GetStatus(), job.GetHandle(), job.GetExitCode())
	}
}

// JobsLs lists jobs known to the daemon. Filter by agent, status,
// or label (repeatable; all labels must match — logical AND).
type JobsLs struct {
	Agent  string   `name:"agent" help:"Filter to one agent (name or ID)"`
	Status string   `name:"status" help:"Filter to one status: pending, running, done, error, cancelled, orphaned"`
	Labels []string `name:"label" help:"Filter by key=value label. Repeatable; all labels must match (AND). Example: --label io.openotters.session-id=cli:chat:abc"`
}

func (j *JobsLs) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	agentID := ""
	if j.Agent != "" {
		agentID, err = resolveAgentID(ctx, c, j.Agent)
		if err != nil {
			return err
		}
	}

	labelSelector, err := parseKVPairs(j.Labels)
	if err != nil {
		return fmt.Errorf("--label: %w", err)
	}

	resp, err := c.ListAsyncJobs(ctx, &daemonv1.ListAsyncJobsRequest{
		AgentId:       agentID,
		Status:        j.Status,
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("list: %w", unwrapRPC(err))
	}
	jobs := resp.GetJobs()
	if len(jobs) == 0 {
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tAGENT\tBIN\tSTATUS\tSTARTED\tDURATION")
	for _, jb := range jobs {
		started := "-"
		duration := "-"
		if jb.GetStartedAt() > 0 {
			st := time.Unix(jb.GetStartedAt(), 0)
			started = st.Format(time.RFC3339)
			end := time.Now()
			if jb.GetFinishedAt() > 0 {
				end = time.Unix(jb.GetFinishedAt(), 0)
			}
			duration = end.Sub(st).Truncate(time.Millisecond).String()
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			jb.GetId(), shortID(jb.GetAgentId()), jb.GetBin(),
			jb.GetStatus(), started, duration)
	}
	_ = w.Flush()
	_ = common
	return nil
}

// JobsInspect prints one job's full state — including stdout, stderr,
// exit code, and timing.
type JobsInspect struct {
	JobID string `arg:"" name:"job-id" help:"Job ID"`
}

func (j *JobsInspect) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.GetAsyncJob(ctx, &daemonv1.GetAsyncJobRequest{JobId: j.JobID})
	if err != nil {
		return fmt.Errorf("get: %w", unwrapRPC(err))
	}
	jb := resp.GetJob()

	p := common.Printer()
	_, _ = p.Printf("ID:         %s\n", jb.GetId())
	_, _ = p.Printf("Agent:      %s\n", jb.GetAgentId())
	_, _ = p.Printf("Bin:        %s\n", jb.GetBin())
	_, _ = p.Printf("Args:       %v\n", jb.GetArgs())
	_, _ = p.Printf("Status:     %s\n", jb.GetStatus())
	if labels := jb.GetLabels(); len(labels) > 0 {
		keys := make([]string, 0, len(labels))
		for k := range labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		_, _ = p.Printf("Labels:\n")
		for _, k := range keys {
			_, _ = p.Printf("  %s=%s\n", k, labels[k])
		}
	}
	if jb.GetHandle() != "" {
		_, _ = p.Printf("Handle:     %s\n", jb.GetHandle())
	}
	if jb.GetStartedAt() > 0 {
		_, _ = p.Printf("Started:    %s\n", time.Unix(jb.GetStartedAt(), 0).Format(time.RFC3339))
	}
	if jb.GetFinishedAt() > 0 {
		_, _ = p.Printf("Finished:   %s\n", time.Unix(jb.GetFinishedAt(), 0).Format(time.RFC3339))
	}
	if isTerminal(jb.GetStatus()) {
		_, _ = p.Printf("Exit code:  %d\n", jb.GetExitCode())
	}
	if jb.GetError() != "" {
		_, _ = p.Printf("\nError:\n%s\n", jb.GetError())
	}
	if jb.GetStdout() != "" {
		_, _ = p.Printf("\nStdout:\n%s\n", jb.GetStdout())
	}
	if jb.GetStderr() != "" {
		_, _ = p.Printf("\nStderr:\n%s\n", jb.GetStderr())
	}
	return nil
}

// JobsCancel cancels a pending or running job. The job still calls
// back to the agent's session — with status=cancelled — so the agent
// learns the job didn't complete.
type JobsCancel struct {
	JobID string `arg:"" name:"job-id" help:"Job ID"`
}

func (j *JobsCancel) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := c.CancelAsyncJob(ctx, &daemonv1.CancelAsyncJobRequest{JobId: j.JobID}); err != nil {
		return fmt.Errorf("cancel: %w", unwrapRPC(err))
	}
	_, _ = common.Printer().Printf("cancelled %s\n", j.JobID)
	return nil
}

// ─── helpers ────────────────────────────────────────────────────────

// resolveAgentID accepts either an agent's UUID (passes through) or
// its display name (resolved via ListAgents). Centralised so every
// jobs subcommand accepts the same agent reference shape as `otters
// run` / `otters chat` do today.
func resolveAgentID(ctx context.Context, c daemonv1.RuntimeClient, ref string) (string, error) {
	if ref == "" {
		return "", errors.New("agent reference is required")
	}
	// Looks like a UUID?
	if len(ref) == 36 && strings.Count(ref, "-") == 4 {
		return ref, nil
	}

	resp, err := c.ListAgents(ctx, &daemonv1.ListAgentsRequest{})
	if err != nil {
		return "", fmt.Errorf("listing agents to resolve %q: %w", ref, unwrapRPC(err))
	}
	for _, a := range resp.GetAgents() {
		if a.GetName() == ref || a.GetId() == ref {
			return a.GetId(), nil
		}
	}
	return "", fmt.Errorf("no running agent matches %q", ref)
}

func readStdinFlag(s string) (string, error) {
	switch {
	case s == "":
		return "", nil
	case s == "-":
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}
		return string(buf), nil
	case strings.HasPrefix(s, "@"):
		buf, err := os.ReadFile(s[1:])
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", s[1:], err)
		}
		return string(buf), nil
	default:
		return s, nil
	}
}

func isTerminal(status string) bool {
	switch status {
	case "done", "error", "cancelled", "orphaned":
		return true
	}
	return false
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
