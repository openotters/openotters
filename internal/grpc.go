package internal

import (
	"context"

	agentpkg "github.com/openotters/agentfile/agent"
	daemonv1 "github.com/openotters/openotters/api/v1"
)

type grpcServer struct {
	daemonv1.UnimplementedRuntimeServer
	daemon *Daemon
}

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

func NewGRPCServer(daemon *Daemon) daemonv1.RuntimeServer {
	return &grpcServer{daemon: daemon}
}

func (s *grpcServer) GetInfo(
	_ context.Context, _ *daemonv1.GetInfoRequest,
) (*daemonv1.GetInfoResponse, error) {
	info := s.daemon.Info()

	return &daemonv1.GetInfoResponse{
		RegistryAddr:  info.RegistryAddr,
		SocketPath:    info.SocketPath,
		LogDir:        info.LogDir,
		AgentsDir:     info.AgentsDir,
		DataDir:       info.DataDir,
		RuntimePath:   info.RuntimePath,
		Version:       info.Version,
		Commit:        info.Commit,
		BuildDate:     info.BuildDate,
		AgentsRunning: safeInt32(info.AgentsRunning),
		AgentsTotal:   safeInt32(info.AgentsTotal),
		Providers:     safeInt32(info.Providers),
	}, nil
}

func (s *grpcServer) BuildAgent(
	ctx context.Context, req *daemonv1.BuildAgentRequest,
) (*daemonv1.BuildAgentResponse, error) {
	return s.daemon.Build(ctx, req)
}

func (s *grpcServer) BuildToolImage(
	ctx context.Context, req *daemonv1.BuildToolImageRequest,
) (*daemonv1.BuildToolImageResponse, error) {
	return s.daemon.BuildTool(ctx, req)
}

func (s *grpcServer) SaveAgentImage(
	ctx context.Context, req *daemonv1.SaveAgentImageRequest,
) (*daemonv1.SaveAgentImageResponse, error) {
	return s.daemon.Save(ctx, req)
}

func (s *grpcServer) PullAgentImage(
	ctx context.Context, req *daemonv1.PullRequest,
) (*daemonv1.PullResponse, error) {
	return s.daemon.Pull(ctx, req)
}

func (s *grpcServer) PushAgentImage(
	ctx context.Context, req *daemonv1.PushRequest,
) (*daemonv1.PushResponse, error) {
	return s.daemon.Push(ctx, req)
}

func (s *grpcServer) ListImages(
	ctx context.Context, req *daemonv1.ListImagesRequest,
) (*daemonv1.ListImagesResponse, error) {
	return s.daemon.ListImages(ctx, req)
}

func (s *grpcServer) RemoveImage(
	ctx context.Context, req *daemonv1.RemoveImageRequest,
) (*daemonv1.RemoveImageResponse, error) {
	return s.daemon.RemoveImage(ctx, req)
}

func (s *grpcServer) DescribeImage(
	ctx context.Context, req *daemonv1.DescribeImageRequest,
) (*daemonv1.DescribeImageResponse, error) {
	return s.daemon.DescribeImage(ctx, req)
}

func (s *grpcServer) CreateAgent(
	ctx context.Context, req *daemonv1.CreateAgentRequest,
) (*daemonv1.CreateAgentResponse, error) {
	return s.daemon.CreateAgent(ctx, req)
}

func (s *grpcServer) ListAgents(
	_ context.Context, _ *daemonv1.ListAgentsRequest,
) (*daemonv1.ListAgentsResponse, error) {
	return &daemonv1.ListAgentsResponse{Agents: s.daemon.List()}, nil
}

func (s *grpcServer) StartAgent(
	ctx context.Context, req *daemonv1.StartAgentRequest,
) (*daemonv1.StartAgentResponse, error) {
	if err := s.daemon.Start(ctx, req.GetRef()); err != nil {
		return nil, err
	}

	return &daemonv1.StartAgentResponse{}, nil
}

func (s *grpcServer) StopAgent(
	ctx context.Context, req *daemonv1.StopAgentRequest,
) (*daemonv1.StopAgentResponse, error) {
	if err := s.daemon.Stop(ctx, req.GetRef()); err != nil {
		return nil, err
	}

	return &daemonv1.StopAgentResponse{}, nil
}

func (s *grpcServer) RemoveAgent(
	ctx context.Context, req *daemonv1.RemoveAgentRequest,
) (*daemonv1.RemoveAgentResponse, error) {
	if err := s.daemon.Remove(ctx, req.GetRef()); err != nil {
		return nil, err
	}

	return &daemonv1.RemoveAgentResponse{}, nil
}

func (s *grpcServer) ChatWithAgent(
	ctx context.Context, req *daemonv1.ChatRequest,
) (*daemonv1.ChatResponse, error) {
	response, err := s.daemon.ChatWithAgent(ctx, req.GetRef(), req.GetSessionId(), req.GetPrompt())
	if err != nil {
		return nil, err
	}

	return &daemonv1.ChatResponse{Response: response}, nil
}

func (s *grpcServer) PromptObject(
	ctx context.Context, req *daemonv1.PromptObjectRequest,
) (*daemonv1.PromptObjectResponse, error) {
	object, err := s.daemon.PromptObjectWithAgent(
		ctx,
		req.GetRef(), req.GetPrompt(), req.GetSchemaJson(),
		req.GetSchemaName(), req.GetSchemaDesc(),
	)
	if err != nil {
		return nil, err
	}

	return &daemonv1.PromptObjectResponse{ObjectJson: object}, nil
}

func (s *grpcServer) ListSessionMessages(
	ctx context.Context, req *daemonv1.ListSessionMessagesRequest,
) (*daemonv1.ListSessionMessagesResponse, error) {
	msgs, err := s.daemon.ListSessionMessages(
		ctx, req.GetRef(), req.GetSessionId(), int(req.GetLimit()),
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

	return &daemonv1.ListSessionMessagesResponse{Messages: out}, nil
}

func (s *grpcServer) ListModels(
	ctx context.Context, _ *daemonv1.ListModelsRequest,
) (*daemonv1.ListModelsResponse, error) {
	rows := s.daemon.Models(ctx)
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

	return &daemonv1.ListModelsResponse{Models: models}, nil
}

func (s *grpcServer) GetAgentLogs(
	_ context.Context, req *daemonv1.GetAgentLogsRequest,
) (*daemonv1.GetAgentLogsResponse, error) {
	content, path, err := s.daemon.AgentLogs(req.GetRef(), req.GetTailBytes(), req.GetTailLines())
	if err != nil {
		return nil, err
	}

	return &daemonv1.GetAgentLogsResponse{Content: content, Path: path}, nil
}

func (s *grpcServer) ChatStreamWithAgent(
	req *daemonv1.ChatStreamRequest, stream daemonv1.Runtime_ChatStreamWithAgentServer,
) error {
	return s.daemon.ChatStreamWithAgent(
		stream.Context(), req.GetRef(), req.GetSessionId(), req.GetPrompt(),
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
