package chatui

import (
	"context"
	"encoding/json"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// jobObservationTools is the set of agent tools whose entire UX point
// is "show me what this job is doing right now". The TUI polls the
// row for stdout / stderr while one of these is in flight so a long
// wait doesn't look hung. Keep in sync with the web's
// JOB_WATCH_TOOLS in app/agents/[agent]/chat/[session]/view.tsx —
// the two surfaces must agree on which tool calls trigger the
// live-log affordance.
//
//nolint:gochecknoglobals // package-private set, no mutation
var jobObservationTools = map[string]struct{}{
	"job_watch":  {},
	"job_wait":   {},
	"job_status": {},
}

// jobLogPollInterval is how often the TUI re-fetches the row while a
// job-observation tool is in flight. Matches the web's 1 s polling
// cadence on /jobs/[job] — close enough to "live" without burning
// the daemon CPU.
const jobLogPollInterval = 1 * time.Second

// jobLogUpdateMsg carries the latest snapshot of a watched job back
// into the bubbletea Update loop. The TUI consumes it to refresh
// the tool block's live-log pane and decide whether to schedule
// another poll tick.
type jobLogUpdateMsg struct {
	jobID    string
	stdout   string
	stderr   string
	terminal bool
	// err is set on RPC failure. The TUI keeps showing whatever
	// state was last good; the error is silently ignored so a flaky
	// daemon doesn't blow up the chat with red banners.
	err error
}

// isJobObservationTool reports whether name is one of the tools the
// TUI watches live. Lowercase exact-match — keep aligned with the
// runtime's tool registrations in runtime/pkg/tool/jobs.go.
func isJobObservationTool(name string) bool {
	_, ok := jobObservationTools[name]

	return ok
}

// extractJobID picks "job_id" out of a tool input envelope. The
// runtime's job_* tools all accept `{"job_id":"job_…"}` (see
// JobIDInput in runtime/pkg/tool/jobs.go). Stream events arrive
// either as the raw inner string or already wrapped in
// `{"input":"…"}`; unwrapToolField + this helper handle both.
func extractJobID(input string) string {
	if input == "" {
		return ""
	}

	var env struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal([]byte(input), &env); err != nil {
		return ""
	}

	return env.JobID
}

// pollJobLogs fetches the current row state for jobID and emits a
// jobLogUpdateMsg. The caller schedules the next tick — keeping the
// re-arm decision (continue while non-terminal, stop on terminal)
// in the Update handler so this Cmd stays a pure one-shot RPC.
//
// Errors are flattened into jobLogUpdateMsg.err rather than
// surfaced via a separate message type; the Update path treats them
// as "no new state this tick" and tries again.
func pollJobLogs(ctx context.Context, client daemonv1.RuntimeClient, jobID string) tea.Cmd {
	return tea.Tick(jobLogPollInterval, func(_ time.Time) tea.Msg {
		// Each tick uses a short-lived ctx derived from the chat's
		// parent so a hung GetAsyncJob can't wedge subsequent ticks.
		rpcCtx, cancel := context.WithTimeout(ctx, jobLogPollInterval*2)
		defer cancel()

		resp, err := client.GetAsyncJob(rpcCtx, &daemonv1.GetAsyncJobRequest{JobId: jobID})
		if err != nil {
			return jobLogUpdateMsg{jobID: jobID, err: err}
		}

		j := resp.GetJob()

		return jobLogUpdateMsg{
			jobID:    jobID,
			stdout:   j.GetStdout(),
			stderr:   j.GetStderr(),
			terminal: isTerminalJobStatus(j.GetStatus()),
		}
	})
}

// isTerminalJobStatus mirrors the daemon's isTerminal() in
// internal/asyncjobs_handlers.go — the four end-of-life states.
// Kept inline rather than imported so the TUI doesn't have to
// depend on the asyncjobs package.
func isTerminalJobStatus(s string) bool {
	switch s {
	case "done", "error", "cancelled", "orphaned":
		return true
	}

	return false
}

// runningJobsPollInterval is how often the bottom-bar refreshes the
// "currently running" list. Slightly slower than the per-tool log
// poller because this is a session-wide rollup — the user only
// needs a heartbeat, not millisecond precision.
const runningJobsPollInterval = 2 * time.Second

// runningJobSnapshot is the trimmed view of one in-flight job the
// bottom bar renders. Keeps the message small so it's cheap to
// pass through the tea loop on every poll.
type runningJobSnapshot struct {
	ID        string
	Bin       string
	CreatedAt time.Time
}

// runningJobsMsg delivers the latest "what's running for this
// session" snapshot to the bubbletea Update loop. Errors flow
// through err and cause the bar to keep showing the last good
// state — no banners.
type runningJobsMsg struct {
	jobs []runningJobSnapshot
	err  error
}

// pollRunningJobs fetches non-terminal jobs labelled with the
// current chat session and emits a runningJobsMsg. Filters
// server-side by status="running" and io.openotters.session-id
// label, so the response is already trimmed to what we want to
// render. The Update handler decides when to re-arm.
func pollRunningJobs(
	ctx context.Context, client daemonv1.RuntimeClient, sessionID string,
) tea.Cmd {
	return tea.Tick(runningJobsPollInterval, func(_ time.Time) tea.Msg {
		rpcCtx, cancel := context.WithTimeout(ctx, runningJobsPollInterval*2)
		defer cancel()

		resp, err := client.ListAsyncJobs(rpcCtx, &daemonv1.ListAsyncJobsRequest{
			Status: "running",
			LabelSelector: map[string]string{
				"io.openotters.session-id": sessionID,
			},
		})
		if err != nil {
			return runningJobsMsg{err: err}
		}

		out := make([]runningJobSnapshot, 0, len(resp.GetJobs()))
		for _, j := range resp.GetJobs() {
			out = append(out, runningJobSnapshot{
				ID:        j.GetId(),
				Bin:       j.GetBin(),
				CreatedAt: time.Unix(j.GetCreatedAt(), 0),
			})
		}

		return runningJobsMsg{jobs: out}
	})
}
