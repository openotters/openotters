package internal

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentpkg "github.com/openotters/agentfile/executor"
	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/api/v1/daemonv1connect"
	"github.com/openotters/openotters/internal/auth"
	"github.com/openotters/openotters/internal/observability"
)

// safeInt32 clamps an int to the int32 range so the proto wire form
// can carry it without an overflow conversion. Counts (running agents,
// providers) never realistically exceed math.MaxInt32 — the clamp is a
// belt-and-braces guard for govet/gosec.
func safeInt32(n int) int32 {
	const maxInt32 = 1<<31 - 1
	if n > maxInt32 {
		return maxInt32
	}

	if n < 0 {
		return 0
	}

	return int32(n)
}

// runtimeHandler satisfies daemonv1connect.RuntimeHandler. The Connect
// migration replaces the old grpcServer; the existing daemon delegations
// are unchanged — only the request/response wrapping shifts from raw
// proto to connect.Request/connect.Response. One source of truth.
type runtimeHandler struct {
	daemonv1connect.UnimplementedRuntimeHandler
	daemon    *Daemon
	providers *ProviderRegistry
}

// NewRuntimeHandler builds the Connect-flavoured handler. providers is
// passed alongside daemon so the provider-mutation RPCs can write
// through to the live in-process cache without a second lookup.
func NewRuntimeHandler(daemon *Daemon, providers *ProviderRegistry) daemonv1connect.RuntimeHandler {
	return &runtimeHandler{daemon: daemon, providers: providers}
}

func (h *runtimeHandler) GetInfo(
	_ context.Context, _ *connect.Request[daemonv1.GetInfoRequest],
) (*connect.Response[daemonv1.GetInfoResponse], error) {
	info := h.daemon.Info()

	return connect.NewResponse(&daemonv1.GetInfoResponse{
		Executor:        info.Executor,
		RegistryAddr:    info.RegistryAddr,
		SocketPath:      info.SocketPath,
		LogDir:          info.LogDir,
		AgentsDir:       info.AgentsDir,
		DataDir:         info.DataDir,
		RuntimePath:     info.RuntimePath,
		Version:         info.Version,
		Commit:          info.Commit,
		BuildDate:       info.BuildDate,
		AgentsRunning:   safeInt32(info.AgentsRunning),
		AgentsTotal:     safeInt32(info.AgentsTotal),
		Providers:       safeInt32(info.Providers),
		MaxConcurrent:   safeInt32(info.MaxConcurrent),
		BackoffBase:     info.BackoffBase.String(),
		BackoffCap:      info.BackoffCap.String(),
		ShutdownTimeout: info.ShutdownTimeout.String(),
	}), nil
}

func (h *runtimeHandler) BuildAgent(
	ctx context.Context, req *connect.Request[daemonv1.BuildAgentRequest],
) (*connect.Response[daemonv1.BuildAgentResponse], error) {
	msg := req.Msg

	// Inline content path — the web UI sends Agentfile bytes directly
	// rather than a daemon-host path. The daemon parses + builds in
	// memory; no temp file lands on disk. ADD / CONTEXT file:// refs
	// won't resolve under this path (the source FS is an empty
	// memfs), but agents that compose themselves from heredoc CONTEXT
	// + registry BIN — what the UI form generates — work fine.
	if len(msg.GetContent()) > 0 {
		resp, err := h.daemon.BuildFromBytes(ctx, msg.GetContent(), msg.GetTags())
		if err != nil {
			return nil, err
		}

		return connect.NewResponse(resp), nil
	}

	resp, err := h.daemon.Build(ctx, msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func (h *runtimeHandler) BuildToolImage(
	ctx context.Context, req *connect.Request[daemonv1.BuildToolImageRequest],
) (*connect.Response[daemonv1.BuildToolImageResponse], error) {
	resp, err := h.daemon.BuildTool(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func (h *runtimeHandler) SaveAgentImage(
	ctx context.Context, req *connect.Request[daemonv1.SaveAgentImageRequest],
) (*connect.Response[daemonv1.SaveAgentImageResponse], error) {
	resp, err := h.daemon.Save(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func (h *runtimeHandler) PullAgentImage(
	ctx context.Context, req *connect.Request[daemonv1.PullRequest],
) (*connect.Response[daemonv1.PullResponse], error) {
	resp, err := h.daemon.Pull(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func (h *runtimeHandler) PushAgentImage(
	ctx context.Context, req *connect.Request[daemonv1.PushRequest],
) (*connect.Response[daemonv1.PushResponse], error) {
	resp, err := h.daemon.Push(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func (h *runtimeHandler) ListImages(
	ctx context.Context, req *connect.Request[daemonv1.ListImagesRequest],
) (*connect.Response[daemonv1.ListImagesResponse], error) {
	resp, err := h.daemon.ListImages(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func (h *runtimeHandler) RemoveImage(
	ctx context.Context, req *connect.Request[daemonv1.RemoveImageRequest],
) (*connect.Response[daemonv1.RemoveImageResponse], error) {
	resp, err := h.daemon.RemoveImage(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func (h *runtimeHandler) RefreshImage(
	ctx context.Context, req *connect.Request[daemonv1.RefreshImageRequest],
) (*connect.Response[daemonv1.RefreshImageResponse], error) {
	resp, err := h.daemon.RefreshImage(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func (h *runtimeHandler) DescribeImage(
	ctx context.Context, req *connect.Request[daemonv1.DescribeImageRequest],
) (*connect.Response[daemonv1.DescribeImageResponse], error) {
	resp, err := h.daemon.DescribeImage(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func (h *runtimeHandler) CreateAgent(
	ctx context.Context, req *connect.Request[daemonv1.CreateAgentRequest],
) (*connect.Response[daemonv1.CreateAgentResponse], error) {
	resp, err := h.daemon.CreateAgent(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func (h *runtimeHandler) ListAgents(
	_ context.Context, req *connect.Request[daemonv1.ListAgentsRequest],
) (*connect.Response[daemonv1.ListAgentsResponse], error) {
	return connect.NewResponse(&daemonv1.ListAgentsResponse{
		Agents: h.daemon.List(req.Msg.GetLabelSelector()),
	}), nil
}

func (h *runtimeHandler) StartAgent(
	ctx context.Context, req *connect.Request[daemonv1.StartAgentRequest],
) (*connect.Response[daemonv1.StartAgentResponse], error) {
	ref := req.Msg.GetRef()
	if err := h.daemon.Start(ctx, ref); err != nil {
		return nil, err
	}

	return connect.NewResponse(&daemonv1.StartAgentResponse{}), nil
}

func (h *runtimeHandler) StopAgent(
	ctx context.Context, req *connect.Request[daemonv1.StopAgentRequest],
) (*connect.Response[daemonv1.StopAgentResponse], error) {
	ref := req.Msg.GetRef()
	if err := h.daemon.Stop(ctx, ref); err != nil {
		return nil, err
	}

	return connect.NewResponse(&daemonv1.StopAgentResponse{}), nil
}

func (h *runtimeHandler) RemoveAgent(
	ctx context.Context, req *connect.Request[daemonv1.RemoveAgentRequest],
) (*connect.Response[daemonv1.RemoveAgentResponse], error) {
	ref := req.Msg.GetRef()
	if err := h.daemon.Remove(ctx, ref); err != nil {
		return nil, err
	}

	return connect.NewResponse(&daemonv1.RemoveAgentResponse{}), nil
}

func (h *runtimeHandler) ChatWithAgent(
	ctx context.Context, req *connect.Request[daemonv1.ChatRequest],
) (*connect.Response[daemonv1.ChatResponse], error) {
	response, err := h.daemon.ChatWithAgent(
		ctx, req.Msg.GetRef(), req.Msg.GetSessionId(), req.Msg.GetPrompt(),
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&daemonv1.ChatResponse{Response: response}), nil
}

func (h *runtimeHandler) PromptObject(
	ctx context.Context, req *connect.Request[daemonv1.PromptObjectRequest],
) (*connect.Response[daemonv1.PromptObjectResponse], error) {
	object, err := h.daemon.PromptObjectWithAgent(
		ctx,
		req.Msg.GetRef(), req.Msg.GetPrompt(), req.Msg.GetSchemaJson(),
		req.Msg.GetSchemaName(), req.Msg.GetSchemaDesc(),
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&daemonv1.PromptObjectResponse{ObjectJson: object}), nil
}

// ChatStreamWithAgent — server-streaming. Connect's stream wrapper
// gives us .Send(&Event); we drive it from the daemon's callback API
// the same way the gRPC version did.
func (h *runtimeHandler) ChatStreamWithAgent(
	ctx context.Context,
	req *connect.Request[daemonv1.ChatStreamRequest],
	stream *connect.ServerStream[daemonv1.ChatStreamEvent],
) error {
	return h.daemon.ChatStreamWithAgent(
		ctx,
		req.Msg.GetRef(),
		req.Msg.GetSessionId(),
		req.Msg.GetPrompt(),
		req.Msg.GetRegenerate(),
		func(ev agentpkg.PromptEvent) {
			_ = stream.Send(&daemonv1.ChatStreamEvent{
				Type:    ev.Type,
				Step:    ev.Step,
				Tool:    ev.Tool,
				Content: ev.Content,
			})
		},
	)
}

func (h *runtimeHandler) ListSessionMessages(
	ctx context.Context, req *connect.Request[daemonv1.ListSessionMessagesRequest],
) (*connect.Response[daemonv1.ListSessionMessagesResponse], error) {
	msgs, err := h.daemon.ListSessionMessages(
		ctx, req.Msg.GetRef(), req.Msg.GetSessionId(), int(req.Msg.GetLimit()),
	)
	if err != nil {
		return nil, err
	}

	out := make([]*daemonv1.SessionMessage, len(msgs))
	for i, m := range msgs {
		out[i] = &daemonv1.SessionMessage{
			Role:         m.Role,
			Content:      m.Content,
			CreatedAt:    m.CreatedAt.Unix(),
			BranchesJson: m.BranchesJSON,
			ActiveBranch: int32(m.ActiveBranch), //nolint:gosec // small int, daemon-bounded
		}
	}

	return connect.NewResponse(&daemonv1.ListSessionMessagesResponse{Messages: out}), nil
}

func (h *runtimeHandler) ListSessions(
	ctx context.Context, req *connect.Request[daemonv1.ListSessionsRequest],
) (*connect.Response[daemonv1.ListSessionsResponse], error) {
	sessions, err := h.daemon.ListSessions(ctx, req.Msg.GetRef())
	if err != nil {
		return nil, err
	}

	out := make([]*daemonv1.SessionInfo, len(sessions))
	for i, s := range sessions {
		out[i] = &daemonv1.SessionInfo{
			Id:           s.ID,
			MessageCount: int32(s.MessageCount), //nolint:gosec // caller-bounded
			LastActive:   s.LastActive.Unix(),
		}
	}

	return connect.NewResponse(&daemonv1.ListSessionsResponse{Sessions: out}), nil
}

func (h *runtimeHandler) DeleteSession(
	ctx context.Context, req *connect.Request[daemonv1.DeleteSessionRequest],
) (*connect.Response[daemonv1.DeleteSessionResponse], error) {
	if err := h.daemon.DeleteSession(ctx, req.Msg.GetRef(), req.Msg.GetSessionId()); err != nil {
		return nil, err
	}

	return connect.NewResponse(&daemonv1.DeleteSessionResponse{}), nil
}

func (h *runtimeHandler) GetAgentLogs(
	_ context.Context, req *connect.Request[daemonv1.GetAgentLogsRequest],
) (*connect.Response[daemonv1.GetAgentLogsResponse], error) {
	content, path, err := h.daemon.AgentLogs(
		req.Msg.GetRef(), req.Msg.GetTailBytes(), req.Msg.GetTailLines(),
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&daemonv1.GetAgentLogsResponse{
		Content: content,
		Path:    path,
	}), nil
}

func (h *runtimeHandler) ListModels(
	ctx context.Context, _ *connect.Request[daemonv1.ListModelsRequest],
) (*connect.Response[daemonv1.ListModelsResponse], error) {
	rows := h.daemon.Models(ctx)
	models := make([]*daemonv1.Model, 0, len(rows))

	for _, m := range rows {
		models = append(models, &daemonv1.Model{
			Provider:         m.Provider,
			Name:             m.Name,
			Ref:              m.Ref,
			ApiBase:          m.APIBase,
			DisplayName:      m.DisplayName,
			ContextWindow:    m.ContextWindow,
			DefaultMaxTokens: m.DefaultMaxTokens,
			CostInputPer_1M:  m.CostInputPer1M,
			CostOutputPer_1M: m.CostOutputPer1M,
			CanReason:        m.CanReason,
		})
	}

	return connect.NewResponse(&daemonv1.ListModelsResponse{Models: models}), nil
}

// Provider mutation handlers — delegate to ProviderRegistry, which owns
// both the on-disk providers.yaml and the in-memory cache. Each
// runtime providers cache is updated in-process when each mutation
// completes, so subsequent ListProviders calls reflect the change
// without waiting for the file's lazy-reload mtime check.

func (h *runtimeHandler) ListProviders(
	_ context.Context, _ *connect.Request[daemonv1.ListProvidersRequest],
) (*connect.Response[daemonv1.ListProvidersResponse], error) {
	if h.providers == nil {
		return connect.NewResponse(&daemonv1.ListProvidersResponse{}), nil
	}

	cfgs := h.providers.Snapshot()
	out := make([]*daemonv1.Provider, 0, len(cfgs))

	for _, cfg := range cfgs {
		out = append(out, providerConfigToProto(cfg))
	}

	return connect.NewResponse(&daemonv1.ListProvidersResponse{Providers: out}), nil
}

// ListAvailableProviders thin-wraps Daemon.AvailableProviders for the
// connect handler. The dashboard's Add Provider form uses it to
// populate a combobox of Catwalk-known slugs. Returns an empty list
// on Catwalk fetch failure rather than an error — the form's
// free-text input is still usable as a fallback.
func (h *runtimeHandler) ListAvailableProviders(
	ctx context.Context, _ *connect.Request[daemonv1.ListAvailableProvidersRequest],
) (*connect.Response[daemonv1.ListAvailableProvidersResponse], error) {
	rows := h.daemon.AvailableProviders(ctx)
	out := make([]*daemonv1.AvailableProvider, 0, len(rows))
	for _, r := range rows {
		out = append(out, &daemonv1.AvailableProvider{
			Id:           r.ID,
			Name:         r.Name,
			ApiEndpoint:  r.APIEndpoint,
			DefaultModel: r.DefaultModel,
			ModelCount:   r.ModelCount,
		})
	}

	return connect.NewResponse(&daemonv1.ListAvailableProvidersResponse{Providers: out}), nil
}

func (h *runtimeHandler) AddProvider(
	_ context.Context, req *connect.Request[daemonv1.AddProviderRequest],
) (*connect.Response[daemonv1.AddProviderResponse], error) {
	if h.providers == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("provider registry not configured"))
	}

	cfg := providerConfigFromProto(req.Msg.GetProvider())
	if cfg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("provider.name is required"))
	}

	saved, err := h.providers.Add(cfg)
	if err != nil {
		return nil, mapProviderError(err)
	}

	return connect.NewResponse(&daemonv1.AddProviderResponse{
		Provider: providerConfigToProto(saved),
	}), nil
}

func (h *runtimeHandler) UpdateProvider(
	_ context.Context, req *connect.Request[daemonv1.UpdateProviderRequest],
) (*connect.Response[daemonv1.UpdateProviderResponse], error) {
	if h.providers == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("provider registry not configured"))
	}

	cfg := providerConfigFromProto(req.Msg.GetProvider())
	if cfg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("provider.name is required"))
	}

	saved, err := h.providers.Update(cfg)
	if err != nil {
		return nil, mapProviderError(err)
	}

	return connect.NewResponse(&daemonv1.UpdateProviderResponse{
		Provider: providerConfigToProto(saved),
	}), nil
}

func (h *runtimeHandler) RemoveProvider(
	_ context.Context, req *connect.Request[daemonv1.RemoveProviderRequest],
) (*connect.Response[daemonv1.RemoveProviderResponse], error) {
	if h.providers == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("provider registry not configured"))
	}

	name := req.Msg.GetName()
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("name is required"))
	}

	if err := h.providers.Remove(name); err != nil {
		return nil, mapProviderError(err)
	}

	return connect.NewResponse(&daemonv1.RemoveProviderResponse{}), nil
}

func providerConfigToProto(cfg ProviderConfig) *daemonv1.Provider {
	models := make([]string, len(cfg.Models))
	copy(models, cfg.Models)

	return &daemonv1.Provider{
		Name:    cfg.Name,
		ApiKey:  cfg.APIKey,
		ApiBase: cfg.APIBase,
		Models:  models,
	}
}

func providerConfigFromProto(p *daemonv1.Provider) ProviderConfig {
	if p == nil {
		return ProviderConfig{}
	}

	models := make([]string, len(p.GetModels()))
	copy(models, p.GetModels())

	return ProviderConfig{
		Name:    p.GetName(),
		APIKey:  p.GetApiKey(),
		APIBase: p.GetApiBase(),
		Models:  models,
	}
}

// mapProviderError translates ProviderRegistry sentinel errors into
// Connect codes so clients can render meaningful messages without
// string-matching.
func mapProviderError(err error) error {
	switch {
	case errors.Is(err, ErrProviderNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrProviderAlreadyExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	default:
		return err
	}
}

// Notes — operator-facing CRUD for an agent's notes. Each handler
// resolves the ref, casts the running agent to executor.NotesAPI,
// and proxies the call through to the per-agent runtime.

func (h *runtimeHandler) ListAgentNotes(
	ctx context.Context, req *connect.Request[daemonv1.ListAgentNotesRequest],
) (*connect.Response[daemonv1.ListAgentNotesResponse], error) {
	all, err := h.daemon.ListAgentNotes(ctx, req.Msg.GetRef(), req.Msg.GetOnlyInContext())
	if err != nil {
		return nil, mapNotesError(err)
	}
	out := make([]*daemonv1.AgentNote, 0, len(all))
	for _, n := range all {
		out = append(out, noteToProto(n))
	}
	return connect.NewResponse(&daemonv1.ListAgentNotesResponse{Notes: out}), nil
}

func (h *runtimeHandler) GetAgentNote(
	ctx context.Context, req *connect.Request[daemonv1.GetAgentNoteRequest],
) (*connect.Response[daemonv1.GetAgentNoteResponse], error) {
	n, err := h.daemon.GetAgentNote(ctx, req.Msg.GetRef(), req.Msg.GetKey())
	if err != nil {
		return nil, mapNotesError(err)
	}
	return connect.NewResponse(&daemonv1.GetAgentNoteResponse{Note: noteToProto(n)}), nil
}

func (h *runtimeHandler) SaveAgentNote(
	ctx context.Context, req *connect.Request[daemonv1.SaveAgentNoteRequest],
) (*connect.Response[daemonv1.SaveAgentNoteResponse], error) {
	res, err := h.daemon.SaveAgentNote(
		ctx, req.Msg.GetRef(), req.Msg.GetKey(), req.Msg.GetContent(),
		req.Msg.GetMaxBytes(), req.Msg.GetMaxCount(),
	)
	if err != nil {
		return nil, mapNotesError(err)
	}
	return connect.NewResponse(&daemonv1.SaveAgentNoteResponse{
		Note:      noteToProto(res.Note),
		Overwrote: res.Overwrote,
	}), nil
}

func (h *runtimeHandler) DeleteAgentNote(
	ctx context.Context, req *connect.Request[daemonv1.DeleteAgentNoteRequest],
) (*connect.Response[daemonv1.DeleteAgentNoteResponse], error) {
	existed, err := h.daemon.DeleteAgentNote(ctx, req.Msg.GetRef(), req.Msg.GetKey())
	if err != nil {
		return nil, mapNotesError(err)
	}
	return connect.NewResponse(&daemonv1.DeleteAgentNoteResponse{Deleted: existed}), nil
}

func (h *runtimeHandler) SetAgentNoteInContext(
	ctx context.Context, req *connect.Request[daemonv1.SetAgentNoteInContextRequest],
) (*connect.Response[daemonv1.SetAgentNoteInContextResponse], error) {
	n, err := h.daemon.SetAgentNoteInContext(
		ctx, req.Msg.GetRef(), req.Msg.GetKey(), req.Msg.GetInContext(),
	)
	if err != nil {
		return nil, mapNotesError(err)
	}
	return connect.NewResponse(&daemonv1.SetAgentNoteInContextResponse{Note: noteToProto(n)}), nil
}

// noteToProto converts an executor.Note into the wire shape. Unix
// timestamps so the TS side can keep notes as plain objects without
// pulling a Timestamp dep.
func noteToProto(n agentpkg.Note) *daemonv1.AgentNote {
	pn := &daemonv1.AgentNote{
		Key:       n.Key,
		Content:   n.Content,
		Preview:   n.Preview,
		InContext: n.InContext,
	}
	if !n.CreatedAt.IsZero() {
		pn.CreatedAt = n.CreatedAt.Unix()
	}
	if !n.UpdatedAt.IsZero() {
		pn.UpdatedAt = n.UpdatedAt.Unix()
	}
	return pn
}

// mapNotesError translates executor sentinels into Connect codes so
// the UI can render meaningful errors without string-matching.
func mapNotesError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, agentpkg.ErrNoteNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return err
}

// StreamRPCCalls tails the daemon's RPC ring buffer. Operator-only
// (rejected for agent tokens) — the firehose carries every call
// the daemon sees, including other agents' traffic, so it's not a
// safe surface for an agent JWT.
//
// The handler first delivers up to req.replay_recent buffered
// events (matching the filters), then streams live ones from the
// subscriber channel until the client disconnects or the
// per-subscriber channel closes. Server-side filtering keeps the
// wire small even when the operator only cares about a slice of
// the traffic.
func (h *runtimeHandler) StreamRPCCalls(
	ctx context.Context,
	req *connect.Request[daemonv1.StreamRPCCallsRequest],
	stream *connect.ServerStream[daemonv1.RPCCallEvent],
) error {
	if c := auth.ClaimsFromContext(ctx); c == nil || c.AgentRef != "" {
		return connect.NewError(connect.CodeUnauthenticated,
			errors.New("StreamRPCCalls requires an operator token"))
	}

	rec := h.daemon.Recorder()
	if rec == nil {
		return connect.NewError(connect.CodeUnavailable,
			errors.New("rpc recorder not configured"))
	}

	filter := buildRPCFilter(req.Msg)
	ch, cancel := rec.Subscribe(int(req.Msg.GetReplayRecent()))
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil
		case call, ok := <-ch:
			if !ok {
				return nil
			}
			if !filter(call) {
				continue
			}
			if err := stream.Send(rpcCallToProto(call)); err != nil {
				return err
			}
		}
	}
}

// buildRPCFilter compiles the request's filter fields into a single
// predicate the streaming loop applies before each Send. Doing the
// filter server-side keeps the bytes-over-the-wire small when the
// operator is staring at a narrow slice of traffic.
func buildRPCFilter(req *daemonv1.StreamRPCCallsRequest) func(observability.RPCCall) bool {
	servicePrefix := req.GetServicePrefix()
	procedurePrefix := req.GetProcedurePrefix()
	callerKind := req.GetCallerKind()
	agentID := req.GetAgentId()
	status := req.GetStatus()
	minDuration := time.Duration(req.GetMinDurationUs()) * time.Microsecond

	return func(c observability.RPCCall) bool {
		if servicePrefix != "" && c.Service != servicePrefix {
			return false
		}
		if procedurePrefix != "" && !strings.HasPrefix(c.Procedure, procedurePrefix) {
			return false
		}
		switch callerKind {
		case "operator":
			if c.Caller != "operator" {
				return false
			}
		case "agent":
			if !strings.HasPrefix(c.Caller, "agent:") {
				return false
			}
			if agentID != "" && c.Caller != "agent:"+agentID {
				return false
			}
		case "anonymous":
			if c.Caller != "anonymous" {
				return false
			}
		}
		switch status {
		case "":
			// no filter
		case "ok":
			if c.Status != "ok" {
				return false
			}
		case "error":
			if c.Status == "ok" {
				return false
			}
		default:
			if c.Status != status {
				return false
			}
		}
		if minDuration > 0 && c.Duration < minDuration {
			return false
		}
		return true
	}
}

// GetAgentIdentity returns an agent's persisted JWT alongside the
// decoded claims. Operator-only — the firehose carries enough to
// impersonate the agent if leaked, so this surface stays off the
// agent JWT scope.
func (h *runtimeHandler) GetAgentIdentity(
	ctx context.Context, req *connect.Request[daemonv1.GetAgentIdentityRequest],
) (*connect.Response[daemonv1.GetAgentIdentityResponse], error) {
	if c := auth.ClaimsFromContext(ctx); c == nil || c.AgentRef != "" {
		return nil, connect.NewError(connect.CodeUnauthenticated,
			errors.New("GetAgentIdentity requires an operator token"))
	}
	if req.Msg.GetRef() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("ref is required"))
	}
	token, claims, err := h.daemon.AgentIdentity(ctx, req.Msg.GetRef())
	if err != nil {
		return nil, err
	}
	out := &daemonv1.GetAgentIdentityResponse{Token: token}
	if claims != nil {
		out.Claims = &daemonv1.AgentIdentityClaims{
			Issuer:       claims.Issuer,
			AgentRef:     claims.AgentRef,
			Jti:          claims.ID,
			Links:        append([]string(nil), claims.Links...),
			Capabilities: append([]string(nil), claims.Capabilities...),
		}
		if claims.IssuedAt != nil {
			out.Claims.IssuedAt = claims.IssuedAt.Unix()
		}
		if claims.ExpiresAt != nil {
			out.Claims.ExpiresAt = claims.ExpiresAt.Unix()
		}
	}
	return connect.NewResponse(out), nil
}

func rpcCallToProto(c observability.RPCCall) *daemonv1.RPCCallEvent {
	return &daemonv1.RPCCallEvent{
		TimestampUnixMs: c.Timestamp.UnixMilli(),
		Service:         c.Service,
		Procedure:       c.Procedure,
		Caller:          c.Caller,
		Status:          c.Status,
		DurationUs:      c.Duration.Microseconds(),
		BytesIn:         c.BytesIn,
		BytesOut:        c.BytesOut,
		ErrMessage:      c.ErrMessage,
		StreamType:      c.StreamType,
	}
}
