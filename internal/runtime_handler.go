package internal

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	agentpkg "github.com/openotters/agentfile/agent"
	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/api/v1/daemonv1connect"
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
	_ context.Context, _ *connect.Request[daemonv1.ListAgentsRequest],
) (*connect.Response[daemonv1.ListAgentsResponse], error) {
	return connect.NewResponse(&daemonv1.ListAgentsResponse{
		Agents: h.daemon.List(),
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
		ctx, req.Msg.GetRef(), req.Msg.GetSessionId(), req.Msg.GetPrompt(),
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
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt.Unix(),
		}
	}

	return connect.NewResponse(&daemonv1.ListSessionMessagesResponse{Messages: out}), nil
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
