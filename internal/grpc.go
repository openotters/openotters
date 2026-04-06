package internal

import (
	"context"
	"fmt"

	daemonv2 "github.com/openotters/cli/api/v1"
	"github.com/openotters/runtime/pkg/agent"
)

type grpcServer struct {
	daemonv2.UnimplementedRuntimeServer
	daemon *Daemon
}

func NewGRPCServer(daemon *Daemon) daemonv2.RuntimeServer {
	return &grpcServer{daemon: daemon}
}

func (s *grpcServer) GetInfo(
	_ context.Context, _ *daemonv2.GetInfoRequest,
) (*daemonv2.GetInfoResponse, error) {
	return &daemonv2.GetInfoResponse{
		RegistryAddr: s.daemon.RegistryAddr(),
	}, nil
}

func (s *grpcServer) SaveAgentImage(
	ctx context.Context, req *daemonv2.SaveAgentImageRequest,
) (*daemonv2.SaveAgentImageResponse, error) {
	return s.daemon.Save(ctx, req)
}

func (s *grpcServer) PullAgentImage(
	ctx context.Context, req *daemonv2.PullRequest,
) (*daemonv2.PullResponse, error) {
	return s.daemon.Pull(ctx, req)
}

func (s *grpcServer) PushAgentImage(
	ctx context.Context, req *daemonv2.PushRequest,
) (*daemonv2.PushResponse, error) {
	return s.daemon.Push(ctx, req)
}

func (s *grpcServer) ListImages(
	ctx context.Context, req *daemonv2.ListImagesRequest,
) (*daemonv2.ListImagesResponse, error) {
	return s.daemon.ListImages(ctx, req)
}

func (s *grpcServer) RemoveImage(
	ctx context.Context, req *daemonv2.RemoveImageRequest,
) (*daemonv2.RemoveImageResponse, error) {
	return s.daemon.RemoveImage(ctx, req)
}

func (s *grpcServer) DescribeImage(
	ctx context.Context, req *daemonv2.DescribeImageRequest,
) (*daemonv2.DescribeImageResponse, error) {
	return s.daemon.DescribeImage(ctx, req)
}

func (s *grpcServer) CreateAgent(
	ctx context.Context, req *daemonv2.CreateAgentRequest,
) (*daemonv2.CreateAgentResponse, error) {
	return s.daemon.Create(ctx, req)
}

func (s *grpcServer) ListAgents(
	_ context.Context, _ *daemonv2.ListAgentsRequest,
) (*daemonv2.ListAgentsResponse, error) {
	return &daemonv2.ListAgentsResponse{Agents: s.daemon.List()}, nil
}

func (s *grpcServer) StopAgent(
	_ context.Context, req *daemonv2.StopAgentRequest,
) (*daemonv2.StopAgentResponse, error) {
	if err := s.daemon.Stop(req.GetRef()); err != nil {
		return nil, err
	}

	return &daemonv2.StopAgentResponse{}, nil
}

func (s *grpcServer) RemoveAgent(
	_ context.Context, req *daemonv2.RemoveAgentRequest,
) (*daemonv2.RemoveAgentResponse, error) {
	if err := s.daemon.Remove(req.GetRef()); err != nil {
		return nil, err
	}

	return &daemonv2.RemoveAgentResponse{}, nil
}

func (s *grpcServer) ChatWithAgent(ctx context.Context, req *daemonv2.ChatRequest) (*daemonv2.ChatResponse, error) {
	ra, err := s.daemon.resolve(req.GetRef())
	if err != nil {
		return nil, err
	}

	if ra.svc == nil {
		return nil, fmt.Errorf("agent %q is %s (model not available)", ra.name, ra.status)
	}

	response, err := ra.svc.Chat(ctx, req.GetSessionId(), req.GetPrompt())
	if err != nil {
		return nil, err
	}

	return &daemonv2.ChatResponse{Response: response}, nil
}

func (s *grpcServer) ChatStreamWithAgent(
	req *daemonv2.ChatStreamRequest, stream daemonv2.Runtime_ChatStreamWithAgentServer,
) error {
	ra, err := s.daemon.resolve(req.GetRef())
	if err != nil {
		return err
	}

	if ra.svc == nil {
		return fmt.Errorf("agent %q is %s (model not available)", ra.name, ra.status)
	}

	cb := func(event agent.StreamEvent) {
		_ = stream.Send(&daemonv2.ChatStreamEvent{
			Type:    event.Type,
			Step:    int32(event.Step), //nolint:gosec // step number is small
			Tool:    event.ToolName,
			Content: event.Content,
		})
	}

	response, err := ra.svc.ChatStream(stream.Context(), req.GetSessionId(), req.GetPrompt(), cb)
	if err != nil {
		return err
	}

	return stream.Send(&daemonv2.ChatStreamEvent{
		Type:    "message.create",
		Content: response,
	})
}
