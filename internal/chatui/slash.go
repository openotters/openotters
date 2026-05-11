package chatui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/pkg"
)

// slashCommand is one entry in the slashRegistry.
type slashCommand struct {
	names []string
	help  string
	// args is a short usage hint for `/help` (e.g. "<id>"); empty
	// for commands that take no arguments.
	args string
	// run takes the rest of the line (everything after the
	// command name, trimmed). It returns the tea.Cmd to schedule
	// and a quit flag.
	run func(args string) (tea.Cmd, bool)
}

// slashRegistry owns the `/foo` commands available at the chat prompt.
type slashRegistry struct {
	theme   *theme
	session func() string
	// parent is the chat's root context; slash RPCs derive
	// short-lived ctxs from it so a Ctrl+C kills any in-flight
	// command cleanly along with the rest of the program.
	parent context.Context //nolint:containedctx // tea callbacks run outside request ctx
	// client is the daemon RPC client. Stored once at construction
	// so individual slash callbacks don't have to thread it.
	client daemonv1.RuntimeClient
	cmds   []slashCommand
}

// newSlashRegistry constructs the registry with the four
// "always-available" commands plus the job-operator commands
// (`/jobs`, `/job`, `/cancel`) wired against client. The parent
// context here is the chat's root — every RPC issued by a slash
// command derives a short-lived child from it so Ctrl+C
// terminates them with the rest of the program.
func newSlashRegistry(
	parent context.Context, t *theme, session func() string, client daemonv1.RuntimeClient,
) *slashRegistry {
	r := &slashRegistry{theme: t, session: session, parent: parent, client: client}

	r.cmds = []slashCommand{
		{
			names: []string{"/quit", "/q", "/exit"},
			help:  "leave the chat",
			run:   func(_ string) (tea.Cmd, bool) { return nil, true },
		},
		{
			names: []string{"/help", "/?"},
			help:  "show this list",
			run:   func(_ string) (tea.Cmd, bool) { return r.Help(), false },
		},
		{
			names: []string{"/clear"},
			help:  "clear the screen",
			run:   func(_ string) (tea.Cmd, bool) { return tea.ClearScreen, false },
		},
		{
			names: []string{"/session"},
			help:  "show the current session id",
			run: func(_ string) (tea.Cmd, bool) {
				return tea.Println("\n" +
					r.theme.infoDot.Render("●") + " " +
					r.theme.dim.Render("session: "+r.session())), false
			},
		},
		{
			names: []string{"/jobs"},
			help:  "list async jobs in this session (optional status filter)",
			args:  "[running|done|error|cancelled|orphaned]",
			run:   r.runListJobs,
		},
		{
			names: []string{"/job"},
			help:  "show one job's state + log tail",
			args:  "<id>",
			run:   r.runShowJob,
		},
		{
			names: []string{"/cancel"},
			help:  "cancel a running async job",
			args:  "<id>",
			run:   r.runCancelJob,
		},
	}

	return r
}

// Dispatch executes the slash command matching line.
// Returns (cmd, quit).
func (r *slashRegistry) Dispatch(line string) (tea.Cmd, bool) {
	head, rest, _ := strings.Cut(line, " ")
	args := strings.TrimSpace(rest)

	for _, c := range r.cmds {
		for _, n := range c.names {
			if n == head {
				return c.run(args)
			}
		}
	}

	return tea.Println("\n" +
		r.theme.errorDot.Render("●") + " " +
		r.theme.errText.Render("unknown command: ") + head), false
}

// Help returns a tea.Cmd that prints the command list.
func (r *slashRegistry) Help() tea.Cmd {
	return tea.Println("\n" + r.renderHelp())
}

func (r *slashRegistry) renderHelp() string {
	var b strings.Builder

	b.WriteString(r.theme.infoDot.Render("●"))
	b.WriteString(" ")
	b.WriteString(r.theme.dim.Render("commands"))
	b.WriteString("\n")

	for _, c := range r.cmds {
		b.WriteString("    ")

		name := strings.Join(c.names, ", ")
		if c.args != "" {
			name += " " + c.args
		}

		b.WriteString(r.theme.toolName.Render(name))
		b.WriteString("  ")
		b.WriteString(r.theme.dim.Render(c.help))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// ─── job-operator commands ────────────────────────────────────────

// slashRPCTimeout caps each slash-command RPC so a hung daemon
// can't wedge the chat input. Generous enough for a healthy local
// daemon, short enough that the user gets a clear error instead
// of staring at an unmoving prompt.
const slashRPCTimeout = 5 * time.Second

// runListJobs implements `/jobs [status]`. Filters by the chat's
// io.openotters.session-id label so the operator sees only the
// work tied to this conversation — the same scoping the agent's
// own job_list tool defaults to. An optional status arg
// narrows further.
func (r *slashRegistry) runListJobs(args string) (tea.Cmd, bool) {
	statusFilter := strings.TrimSpace(args)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(r.parent, slashRPCTimeout)
		defer cancel()

		req := &daemonv1.ListAsyncJobsRequest{
			LabelSelector: map[string]string{
				"io.openotters.session-id": r.session(),
			},
			Status: statusFilter,
		}

		resp, err := r.client.ListAsyncJobs(ctx, req)
		if err != nil {
			return slashOutputMsg{text: r.renderRPCError("/jobs", err)}
		}

		return slashOutputMsg{text: r.renderJobsTable(resp.GetJobs())}
	}, false
}

// runShowJob implements `/job <id>` — single-job snapshot with a
// trimmed stdout/stderr tail. Useful when /jobs surfaced an id and
// the operator wants the body without spawning the agent.
func (r *slashRegistry) runShowJob(args string) (tea.Cmd, bool) {
	id := strings.TrimSpace(args)
	if id == "" {
		return r.errMsg("/job: missing job id (try /jobs to list)"), false
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(r.parent, slashRPCTimeout)
		defer cancel()

		resp, err := r.client.GetAsyncJob(ctx, &daemonv1.GetAsyncJobRequest{JobId: id})
		if err != nil {
			return slashOutputMsg{text: r.renderRPCError("/job", err)}
		}

		return slashOutputMsg{text: r.renderJobDetail(resp.GetJob())}
	}, false
}

// runCancelJob implements `/cancel <id>`. The daemon returns
// FailedPrecondition for already-terminal jobs; that's surfaced
// verbatim — the operator should know the call did nothing.
func (r *slashRegistry) runCancelJob(args string) (tea.Cmd, bool) {
	id := strings.TrimSpace(args)
	if id == "" {
		return r.errMsg("/cancel: missing job id"), false
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(r.parent, slashRPCTimeout)
		defer cancel()

		if _, err := r.client.CancelAsyncJob(ctx, &daemonv1.CancelAsyncJobRequest{JobId: id}); err != nil {
			return slashOutputMsg{text: r.renderRPCError("/cancel", err)}
		}

		return slashOutputMsg{text: "\n" +
			r.theme.infoDot.Render("●") + " " +
			r.theme.dim.Render("cancel requested for "+id)}
	}, false
}

// errMsg wraps a literal error string in the chat's standard
// error-line format. Used for argument-validation failures that
// don't need an RPC.
func (r *slashRegistry) errMsg(text string) tea.Cmd {
	return tea.Println("\n" +
		r.theme.errorDot.Render("●") + " " +
		r.theme.errText.Render(text))
}

// renderRPCError formats a daemon RPC failure as a one-line error
// row. Uses pkg.UnwrapRPC so Connect/gRPC wrappers don't leak
// implementation chatter into the chat.
func (r *slashRegistry) renderRPCError(cmd string, err error) string {
	return "\n" +
		r.theme.errorDot.Render("●") + " " +
		r.theme.errText.Render(cmd+": "+pkg.UnwrapRPC(err).Error())
}

// renderJobsTable renders the /jobs result as a compact table.
// Columns: id (last 12 chars) · status · bin · age. Empty list
// gets an explicit "no jobs" line so the user doesn't wonder
// whether the RPC actually fired.
func (r *slashRegistry) renderJobsTable(jobs []*daemonv1.AsyncJob) string {
	if len(jobs) == 0 {
		return "\n" +
			r.theme.infoDot.Render("●") + " " +
			r.theme.dim.Render("no jobs in this session")
	}

	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(r.theme.infoDot.Render("●"))
	b.WriteString(" ")
	b.WriteString(r.theme.dim.Render(fmt.Sprintf("%d job(s) in this session", len(jobs))))
	b.WriteString("\n")

	for _, j := range jobs {
		b.WriteString("    ")
		b.WriteString(r.theme.toolName.Render(j.GetId()))
		b.WriteString("  ")
		b.WriteString(r.theme.toolResult.Render(j.GetStatus()))
		b.WriteString("  ")
		b.WriteString(r.theme.toolInput.Render(j.GetBin()))
		b.WriteString("  ")
		b.WriteString(r.theme.dim.Render(jobAge(j)))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// renderJobDetail formats a single job for /job. Includes status,
// exit code (when terminal), and a trimmed stdout/stderr tail
// (last 10 lines each) so a noisy build log doesn't drown the
// chat.
func (r *slashRegistry) renderJobDetail(j *daemonv1.AsyncJob) string {
	if j == nil {
		return r.theme.errorDot.Render("●") + " " +
			r.theme.errText.Render("/job: empty response")
	}

	const tail = 10

	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(r.theme.infoDot.Render("●"))
	b.WriteString(" ")
	b.WriteString(r.theme.toolName.Render(j.GetId()))
	b.WriteString("  ")
	b.WriteString(r.theme.toolResult.Render(j.GetStatus()))

	if isTerminalJobStatus(j.GetStatus()) {
		b.WriteString("  ")
		b.WriteString(r.theme.dim.Render(fmt.Sprintf("exit=%d", j.GetExitCode())))
	}

	b.WriteString("  ")
	b.WriteString(r.theme.dim.Render(j.GetBin()))
	b.WriteString("\n")

	if out := j.GetStdout(); out != "" {
		b.WriteString("  ")
		b.WriteString(r.theme.treeMark.Render("⎿ stdout"))
		b.WriteString("\n")
		appendTail(&b, r.theme, out, tail)
	}

	if errOut := j.GetStderr(); errOut != "" {
		b.WriteString("  ")
		b.WriteString(r.theme.treeMark.Render("⎿ stderr"))
		b.WriteString("\n")
		appendTail(&b, r.theme, errOut, tail)
	}

	if j.GetError() != "" {
		b.WriteString("  ")
		b.WriteString(r.theme.errorDot.Render("⎿ error"))
		b.WriteString(" ")
		b.WriteString(r.theme.errText.Render(j.GetError()))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// appendTail writes the last n lines of body to b, indented to
// hang under a "⎿" marker. Lines are NOT truncated horizontally —
// the renderer doesn't know the terminal width here; the operator
// can resize.
func appendTail(b *strings.Builder, t *theme, body string, n int) {
	for _, line := range tailLines(body, n) {
		b.WriteString("    ")
		b.WriteString(t.toolResult.Render(line))
		b.WriteString("\n")
	}
}

// jobAge formats a job's age relative to now. Reads created_at off
// the proto and falls back to "?" when missing (Daemon < some
// version, or a row that somehow has no timestamp).
func jobAge(j *daemonv1.AsyncJob) string {
	if j == nil || j.GetCreatedAt() == 0 {
		return "?"
	}

	d := time.Since(time.Unix(j.GetCreatedAt(), 0))

	return fmtElapsedSeconds(d.Round(time.Second)) + " ago"
}

// slashOutputMsg carries the rendered output of a slash command
// back into the bubbletea Update path so the model can wrap it in
// a tea.Println (which is the only way to push lines into the
// scrollback during a live program). Defined here rather than in
// stream.go because it's a slash-specific signal — the stream
// reader doesn't produce it.
type slashOutputMsg struct {
	text string
}
