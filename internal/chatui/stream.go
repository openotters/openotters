package chatui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/pkg"
)

// Stream message types delivered from the gRPC read goroutine into the
// bubbletea Update pipeline.

type streamStartedMsg struct {
	stream daemonv1.Runtime_ChatStreamWithAgentClient
	cancel context.CancelFunc
}

type streamEventMsg struct {
	event  *daemonv1.ChatStreamEvent
	stream daemonv1.Runtime_ChatStreamWithAgentClient
}

type streamDoneMsg struct{}

type streamErrorMsg struct{ err error }

// historyLoadedMsg delivers preloaded user prompts from the daemon's
// ListSessionMessages RPC into the chat model.
type historyLoadedMsg struct {
	prompts []string
}

// historyLoadErrorMsg signals that the preload failed (agent stopped,
// RPC missing, network hiccup). The model logs nothing and carries on
// with an empty ring.
type historyLoadErrorMsg struct{ err error }

// loadHistory returns a tea.Cmd that asks the daemon for prior user
// prompts in (ref, sessionID) and emits a historyLoadedMsg on success,
// historyLoadErrorMsg on failure.
func loadHistory(
	ctx context.Context, client daemonv1.RuntimeClient, ref, sessionID string,
) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.ListSessionMessages(ctx, &daemonv1.ListSessionMessagesRequest{
			Ref:       ref,
			SessionId: sessionID,
		})
		if err != nil {
			return historyLoadErrorMsg{err: pkg.UnwrapRPC(err)}
		}

		var prompts []string

		for _, m := range resp.GetMessages() {
			if m.GetRole() == "user" {
				prompts = append(prompts, m.GetContent())
			}
		}

		return historyLoadedMsg{prompts: prompts}
	}
}

// openStream returns a tea.Cmd that opens the daemon ChatStream.
func openStream(
	ctx context.Context, client daemonv1.RuntimeClient, req *daemonv1.ChatStreamRequest,
) tea.Cmd {
	return func() tea.Msg {
		streamCtx, cancel := context.WithCancel(ctx)

		stream, err := client.ChatStreamWithAgent(streamCtx, req)
		if err != nil {
			cancel()

			return streamErrorMsg{err: pkg.UnwrapRPC(err)}
		}

		return streamStartedMsg{stream: stream, cancel: cancel}
	}
}

// recvNext returns a tea.Cmd that reads the next event from stream.
func recvNext(stream daemonv1.Runtime_ChatStreamWithAgentClient) tea.Cmd {
	return func() tea.Msg {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return streamDoneMsg{}
		}

		if err != nil {
			return streamErrorMsg{err: pkg.UnwrapRPC(err)}
		}

		return streamEventMsg{event: ev, stream: stream}
	}
}

// unwrapToolField extracts a single string field from a JSON envelope
// like {"input": "..."} or {"output": "..."}. Returns the raw payload
// if it isn't JSON-shaped.
func unwrapToolField(raw, field string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	if !strings.HasPrefix(trimmed, "{") {
		return trimmed
	}

	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return trimmed
	}

	val, ok := env[field]
	if !ok {
		return trimmed
	}

	var str string
	if err := json.Unmarshal(val, &str); err == nil {
		return strings.TrimSpace(str)
	}

	return strings.TrimSpace(string(val))
}
