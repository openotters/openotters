// Async-jobs Connect handlers — separate file to keep
// runtime_handler.go focused on the older lifecycle / image RPCs.
// Each handler is a thin wrapper around the daemon's asyncjobs.Pool;
// the actual dispatch / cancel / read logic lives in
// internal/asyncjobs.
package internal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/internal/asyncjobs"
	"github.com/openotters/openotters/internal/auth"
)

// scopedAgentID returns the agent_id the JWT pins this caller to,
// or "" for operator tokens (admin scope). Agent-scoped tokens
// always carry AgentRef in the validated claims.
func scopedAgentID(ctx context.Context) string {
	if claims := auth.ClaimsFromContext(ctx); claims != nil {
		return claims.AgentRef
	}
	return ""
}

// assertOwnedBy verifies the caller can see jobs belonging to
// `agentID`. Operator tokens see everything (no claim → empty
// scope). Agent-scoped tokens only see their own agent's jobs;
// cross-agent reads return CodeNotFound so existence isn't leaked
// (would otherwise be PermissionDenied — but that confirms the row
// exists, which is itself information).
func assertOwnedBy(ctx context.Context, agentID string) *connect.Error {
	scope := scopedAgentID(ctx)
	if scope == "" || scope == agentID {
		return nil
	}
	return connect.NewError(connect.CodeNotFound, asyncjobs.ErrNotFound)
}

// assertJobInScope loads a job and runs assertOwnedBy on its
// agent_id. Used by Cancel which doesn't otherwise need the row's
// content — just need to make sure the caller is allowed to touch
// it. Returns CodeNotFound when the job doesn't exist OR isn't in
// the caller's scope (uniform response so existence isn't leaked).
func (h *runtimeHandler) assertJobInScope(ctx context.Context, jobID string) *connect.Error {
	if jobID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("job_id is required"))
	}
	store := asyncjobs.NewStore(h.daemon.state.db)
	j, err := store.Get(ctx, jobID)
	if errors.Is(err, asyncjobs.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	return assertOwnedBy(ctx, j.AgentID)
}

// boundAgentRef is the per-handler scoping helper. Agent-scoped
// tokens (those carrying AgentRef in claims) ALWAYS get pinned to
// their own agent — the request's agent_ref / agent_id field is
// ignored. Operator tokens (admin) pass through whatever the request
// asks for. This is what prevents agent A from submitting jobs as
// agent B even if it forges agent_ref in the wire request.
//
// Returns (effectiveID, true) when the request should proceed with
// `effectiveID` as the canonical agent identity. Returns ("", false)
// when neither the JWT nor the request supplied one — caller maps
// that to InvalidArgument.
func boundAgentRef(ctx context.Context, fromRequest string) (string, bool) {
	if claims := auth.ClaimsFromContext(ctx); claims != nil && claims.AgentRef != "" {
		return claims.AgentRef, true
	}
	if fromRequest != "" {
		return fromRequest, true
	}
	return "", false
}

// watchPollInterval is how often WatchAsyncJob polls the row. Tight
// enough that operators see status flips quickly; loose enough that
// 100 concurrent watchers don't pummel SQLite. If pubsub-style
// notification ever lands inside the Pool, this becomes a fallback
// floor rather than the primary mechanism.
const watchPollInterval = 250 * time.Millisecond

func (h *runtimeHandler) SubmitAsyncJob(
	ctx context.Context, req *connect.Request[daemonv1.SubmitAsyncJobRequest],
) (*connect.Response[daemonv1.SubmitAsyncJobResponse], error) {
	ref, ok := boundAgentRef(ctx, req.Msg.GetAgentRef())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("agent_ref is required"))
	}

	// Resolve via the daemon's name-or-UUID resolver — same path
	// ChatWithAgent uses, so callers can hand us either form.
	ma, err := h.daemon.resolve(ref)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	agentID := ma.id.String()

	// Validate the BIN is declared in the agent's spawn env BEFORE
	// creating a row + dispatching. Bouncing at submit means the
	// caller sees the failure synchronously, not as a delayed
	// `error`-status row 5 seconds later. The executor backends ALSO
	// check (defence-in-depth for non-daemon callers), but this is
	// where users get the cleanest signal.
	bin := req.Msg.GetBin()
	if bin == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("bin is required"))
	}
	if vErr := h.validateAgentBin(agentID, bin); vErr != nil {
		return nil, vErr
	}

	id, err := h.daemon.AsyncJobs().Submit(ctx, asyncjobs.Spec{
		AgentID: agentID,
		Bin:     bin,
		Args:    req.Msg.GetArgs(),
		Stdin:   req.Msg.GetStdin(),
		Labels:  req.Msg.GetLabels(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&daemonv1.SubmitAsyncJobResponse{JobId: id}), nil
}

// validateAgentBin returns a connect-typed error when `bin` isn't
// declared in the running agent's tool set. The two failure modes
// (agent not running / agent not initialised) both map to
// FailedPrecondition since they're recoverable by the operator
// (start the agent / wait for Prepare); the BIN-not-declared case
// is InvalidArgument since the only fix is to amend the Agentfile
// or pick a different bin. Available BIN names are listed verbatim
// in the error so the operator doesn't have to crack open the
// Agentfile to find them.
func (h *runtimeHandler) validateAgentBin(agentIDStr, bin string) *connect.Error {
	agentUUID, err := uuid.Parse(agentIDStr)
	if err != nil {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("agent_id is not a valid UUID: %w", err))
	}
	agent, ok := h.daemon.pool.Get(agentUUID)
	if !ok {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("agent %s is not running — start it before submitting jobs", agentIDStr))
	}
	rt := agent.Runtime()
	if rt == nil {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("agent %s is not initialised yet — wait for it to reach running state", agentIDStr))
	}

	for _, t := range rt.Tools {
		if t.Name == bin {
			return nil
		}
	}

	names := make([]string, 0, len(rt.Tools))
	for _, t := range rt.Tools {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	available := "(none)"
	if len(names) > 0 {
		available = strings.Join(names, ", ")
	}

	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
		"BIN %q is not declared in agent %s — add `BIN %s <ref>` to its Agentfile and rebuild, or pick one of: %s",
		bin, agentIDStr, bin, available,
	))
}

func (h *runtimeHandler) CancelAsyncJob(
	ctx context.Context, req *connect.Request[daemonv1.CancelAsyncJobRequest],
) (*connect.Response[daemonv1.CancelAsyncJobResponse], error) {
	jobID := req.Msg.GetJobId()
	if scopeErr := h.assertJobInScope(ctx, jobID); scopeErr != nil {
		return nil, scopeErr
	}
	err := h.daemon.AsyncJobs().Cancel(jobID)
	if errors.Is(err, asyncjobs.ErrNotRunning) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&daemonv1.CancelAsyncJobResponse{}), nil
}

func (h *runtimeHandler) GetAsyncJob(
	ctx context.Context, req *connect.Request[daemonv1.GetAsyncJobRequest],
) (*connect.Response[daemonv1.GetAsyncJobResponse], error) {
	store := asyncjobs.NewStore(h.daemon.state.db)
	j, err := store.Get(ctx, req.Msg.GetJobId())
	if errors.Is(err, asyncjobs.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Agent-scoped tokens only see their own jobs. Map cross-agent
	// reads to NotFound so existence isn't leaked across the
	// boundary.
	if scopeErr := assertOwnedBy(ctx, j.AgentID); scopeErr != nil {
		return nil, scopeErr
	}
	return connect.NewResponse(&daemonv1.GetAsyncJobResponse{Job: jobToProto(j)}), nil
}

func (h *runtimeHandler) ListAsyncJobs(
	ctx context.Context, req *connect.Request[daemonv1.ListAsyncJobsRequest],
) (*connect.Response[daemonv1.ListAsyncJobsResponse], error) {
	store := asyncjobs.NewStore(h.daemon.state.db)

	filter := asyncjobs.ListFilter{Labels: req.Msg.GetLabelSelector()}
	if s := req.Msg.GetStatus(); s != "" {
		filter.Statuses = []asyncjobs.Status{asyncjobs.Status(s)}
	}

	// Agent-scoped tokens get pinned to their own agent_id — request
	// filter is honored as far as it MATCHES the bound agent
	// (operator can ask for "my agent X" but it's always going to be
	// X). Operator tokens fall through with whatever the request sets.
	scopeAgent := scopedAgentID(ctx)
	requestedAgent := req.Msg.GetAgentId()
	effectiveAgent := requestedAgent
	if scopeAgent != "" {
		effectiveAgent = scopeAgent
	}

	var jobs []*asyncjobs.Job
	var err error
	if effectiveAgent != "" {
		jobs, err = store.ListByAgent(ctx, effectiveAgent, filter)
	} else {
		jobs, err = store.ListAll(ctx, filter)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	out := make([]*daemonv1.AsyncJob, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobToProto(j))
	}
	return connect.NewResponse(&daemonv1.ListAsyncJobsResponse{Jobs: out}), nil
}

// WatchAsyncJob streams the row each time it materially changes,
// closing the stream on terminal status (done / error / cancelled /
// orphaned). The first message is sent immediately so the caller
// always has a snapshot of the current state — including for jobs
// that already finished before the watch started (one message, then
// EOF).
//
// Implementation: server-side poll. Simple, correct, scales fine
// at v1 expected concurrency. If the pool ever exposes
// pubsub-style change notifications, swap the ticker for them
// without changing this RPC's contract.
func (h *runtimeHandler) WatchAsyncJob(
	ctx context.Context,
	req *connect.Request[daemonv1.WatchAsyncJobRequest],
	stream *connect.ServerStream[daemonv1.WatchAsyncJobResponse],
) error {
	jobID := req.Msg.GetJobId()
	if jobID == "" {
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("job_id is required"))
	}

	store := asyncjobs.NewStore(h.daemon.state.db)

	// First fetch — confirms existence (NotFound at start), and seeds
	// the change-detection comparator. Always send the first snapshot
	// so callers don't have to wait a tick to see the current state.
	current, err := store.Get(ctx, jobID)
	if errors.Is(err, asyncjobs.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	// Same scope check as Get — agent-scoped tokens watching another
	// agent's job get NotFound, no info leak.
	if scopeErr := assertOwnedBy(ctx, current.AgentID); scopeErr != nil {
		return scopeErr
	}
	if sendErr := stream.Send(&daemonv1.WatchAsyncJobResponse{Job: jobToProto(current)}); sendErr != nil {
		return sendErr
	}
	if isTerminal(current.Status) {
		return nil
	}

	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Client disconnect / timeout — Connect surfaces this as
			// an error to the caller; nothing for us to do.
			return ctx.Err()
		case <-ticker.C:
			next, getErr := store.Get(ctx, jobID)
			if errors.Is(getErr, asyncjobs.ErrNotFound) {
				// Row vanished mid-watch (FK cascade after the agent
				// was removed). Surface explicitly so the caller can
				// distinguish from a clean terminal close.
				return connect.NewError(connect.CodeNotFound,
					fmt.Errorf("job %q vanished while watching — likely the agent was removed", jobID))
			}
			if getErr != nil {
				return connect.NewError(connect.CodeInternal, getErr)
			}

			if !materiallyChanged(current, next) {
				continue
			}

			if sendErr := stream.Send(&daemonv1.WatchAsyncJobResponse{Job: jobToProto(next)}); sendErr != nil {
				return sendErr
			}
			current = next

			if isTerminal(next.Status) {
				return nil
			}
		}
	}
}

// isTerminal reports whether a status means the job is done. Mirrors
// the CHECK constraint on async_jobs.status; the pending / running
// pair are the only non-terminal values.
func isTerminal(s asyncjobs.Status) bool {
	switch s {
	case asyncjobs.StatusDone,
		asyncjobs.StatusError,
		asyncjobs.StatusCancelled,
		asyncjobs.StatusOrphaned:
		return true
	case asyncjobs.StatusPending, asyncjobs.StatusRunning:
		return false
	}
	return false
}

// materiallyChanged compares the mutable subset of two job rows.
// Static fields (id / agent_id / bin / args / stdin / labels /
// created_at) never change post-insert, so equality on the
// remaining fields is sufficient — and avoids spurious sends every
// poll tick when nothing has actually moved.
func materiallyChanged(a, b *asyncjobs.Job) bool {
	if a == nil || b == nil {
		return a != b
	}
	return a.Status != b.Status ||
		a.Handle != b.Handle ||
		a.ExitCode != b.ExitCode ||
		a.Stdout != b.Stdout ||
		a.Stderr != b.Stderr ||
		a.Error != b.Error ||
		a.StartedAt != b.StartedAt ||
		a.FinishedAt != b.FinishedAt
}

// jobToProto maps the storage row to the wire shape.
func jobToProto(j *asyncjobs.Job) *daemonv1.AsyncJob {
	pb := &daemonv1.AsyncJob{
		Id:        j.ID,
		AgentId:   j.AgentID,
		Bin:       j.Bin,
		Args:      j.Args,
		Stdin:     j.Stdin,
		Labels:    j.Labels,
		Status:    string(j.Status),
		Handle:    j.Handle,
		Stdout:    j.Stdout,
		Stderr:    j.Stderr,
		Error:     j.Error,
		CreatedAt: j.CreatedAt.Unix(),
	}
	if j.ExitCode.Valid {
		pb.ExitCode = safeInt32(int(j.ExitCode.Int64))
	}
	if j.StartedAt.Valid {
		pb.StartedAt = j.StartedAt.Time.Unix()
	}
	if j.FinishedAt.Valid {
		pb.FinishedAt = j.FinishedAt.Time.Unix()
	}
	return pb
}
