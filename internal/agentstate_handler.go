package internal

// agentstate_handler.go wires the AgentState Connect service to
// the per-agent CRUD methods on StateStore. Authentication +
// agent-scoping is done by the auth.AgentScopedInterceptor
// registered alongside this handler in serve.go — every method
// here reads the agent_id from the JWT claims via
// auth.AgentIDFromContext and never trusts a request field for
// it.

import (
	"context"
	"database/sql"
	"errors"

	"connectrpc.com/connect"
	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/api/v1/daemonv1connect"
	"github.com/openotters/openotters/internal/auth"
)

// agentStateHandler implements daemonv1connect.AgentStateHandler.
// All methods share the same shape: read agent_id from claims,
// delegate to StateStore, translate sentinel errors to gRPC codes.
type agentStateHandler struct {
	daemonv1connect.UnimplementedAgentStateHandler
	state *StateStore
}

func NewAgentStateHandler(state *StateStore) daemonv1connect.AgentStateHandler {
	return &agentStateHandler{state: state}
}

// agentID is the one-line accessor each handler uses. The
// AgentScopedInterceptor guarantees the claim is non-empty by
// the time we read it; returning Internal here would mean the
// interceptor was bypassed.
func agentID(ctx context.Context) (string, error) {
	id := auth.AgentIDFromContext(ctx)
	if id == "" {
		return "", connect.NewError(connect.CodeInternal,
			errors.New("agent_id missing from claims — interceptor misconfigured?"))
	}
	return id, nil
}

// ── Messages ─────────────────────────────────────────────────────

func (h *agentStateHandler) ListMessages(
	ctx context.Context, req *connect.Request[daemonv1.StateListMessagesRequest],
) (*connect.Response[daemonv1.StateListMessagesResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := h.state.AgentMessagesList(ctx, aid, req.Msg.GetSessionId(), req.Msg.GetLimit())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*daemonv1.MessageRow, 0, len(rows))
	for _, m := range rows {
		out = append(out, messageRowToProto(m))
	}
	return connect.NewResponse(&daemonv1.StateListMessagesResponse{Messages: out}), nil
}

func (h *agentStateHandler) AppendMessage(
	ctx context.Context, req *connect.Request[daemonv1.StateAppendMessageRequest],
) (*connect.Response[daemonv1.StateAppendMessageResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := h.state.AgentMessagesAppend(
		ctx, aid, req.Msg.GetSessionId(), req.Msg.GetRole(), req.Msg.GetContent(),
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&daemonv1.StateAppendMessageResponse{Id: id}), nil
}

func (h *agentStateHandler) ReplaceMessages(
	ctx context.Context, req *connect.Request[daemonv1.StateReplaceMessagesRequest],
) (*connect.Response[daemonv1.StateReplaceMessagesResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]AgentMessageRow, 0, len(req.Msg.GetMessages()))
	for _, m := range req.Msg.GetMessages() {
		rows = append(rows, messageRowFromProto(m))
	}
	if execErr := h.state.AgentMessagesReplace(ctx, aid, req.Msg.GetSessionId(), rows); execErr != nil {
		return nil, connect.NewError(connect.CodeInternal, execErr)
	}
	return connect.NewResponse(&daemonv1.StateReplaceMessagesResponse{}), nil
}

func (h *agentStateHandler) UpdateMessageBranches(
	ctx context.Context, req *connect.Request[daemonv1.StateUpdateBranchesRequest],
) (*connect.Response[daemonv1.StateUpdateBranchesResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	if upErr := h.state.AgentMessagesUpdateBranches(
		ctx, aid, req.Msg.GetId(),
		req.Msg.GetContent(), req.Msg.GetBranchesJson(), req.Msg.GetActiveBranch(),
	); upErr != nil {
		if errors.Is(upErr, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("message id not found for this agent"))
		}
		return nil, connect.NewError(connect.CodeInternal, upErr)
	}
	return connect.NewResponse(&daemonv1.StateUpdateBranchesResponse{}), nil
}

func (h *agentStateHandler) LastAssistantMessage(
	ctx context.Context, req *connect.Request[daemonv1.StateLastAssistantRequest],
) (*connect.Response[daemonv1.StateLastAssistantResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	m, err := h.state.AgentMessagesLastAssistant(ctx, aid, req.Msg.GetSessionId())
	if errors.Is(err, sql.ErrNoRows) {
		return connect.NewResponse(&daemonv1.StateLastAssistantResponse{Found: false}), nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&daemonv1.StateLastAssistantResponse{
		Message: messageRowToProto(m),
		Found:   true,
	}), nil
}

func (h *agentStateHandler) CountMessages(
	ctx context.Context, req *connect.Request[daemonv1.StateCountMessagesRequest],
) (*connect.Response[daemonv1.StateCountMessagesResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	n, err := h.state.AgentMessagesCount(ctx, aid, req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&daemonv1.StateCountMessagesResponse{Count: n}), nil
}

// ── Sessions ─────────────────────────────────────────────────────

func (h *agentStateHandler) ListSessions(
	ctx context.Context, _ *connect.Request[daemonv1.StateListSessionsRequest],
) (*connect.Response[daemonv1.StateListSessionsResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	sessions, err := h.state.AgentSessionsList(ctx, aid)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*daemonv1.StateSessionInfo, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, &daemonv1.StateSessionInfo{
			Id:           s.ID,
			MessageCount: s.MessageCount,
			LastActive:   s.LastActive.Unix(),
		})
	}
	return connect.NewResponse(&daemonv1.StateListSessionsResponse{Sessions: out}), nil
}

func (h *agentStateHandler) DeleteSession(
	ctx context.Context, req *connect.Request[daemonv1.StateDeleteSessionRequest],
) (*connect.Response[daemonv1.StateDeleteSessionResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	if delErr := h.state.AgentSessionsDelete(ctx, aid, req.Msg.GetSessionId()); delErr != nil {
		return nil, connect.NewError(connect.CodeInternal, delErr)
	}
	return connect.NewResponse(&daemonv1.StateDeleteSessionResponse{}), nil
}

// ── Notes ────────────────────────────────────────────────────────

func (h *agentStateHandler) ListNotes(
	ctx context.Context, req *connect.Request[daemonv1.StateListNotesRequest],
) (*connect.Response[daemonv1.StateListNotesResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	notes, err := h.state.AgentNotesList(ctx, aid, req.Msg.GetOnlyInContext())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*daemonv1.NoteRow, 0, len(notes))
	for _, n := range notes {
		out = append(out, noteRowToProto(n))
	}
	return connect.NewResponse(&daemonv1.StateListNotesResponse{Notes: out}), nil
}

func (h *agentStateHandler) GetNote(
	ctx context.Context, req *connect.Request[daemonv1.StateGetNoteRequest],
) (*connect.Response[daemonv1.StateGetNoteResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	n, err := h.state.AgentNotesGet(ctx, aid, req.Msg.GetKey())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("note not found"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&daemonv1.StateGetNoteResponse{Note: noteRowToProto(n)}), nil
}

func (h *agentStateHandler) SaveNote(
	ctx context.Context, req *connect.Request[daemonv1.StateSaveNoteRequest],
) (*connect.Response[daemonv1.StateSaveNoteResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	saved, overwrote, err := h.state.AgentNotesSave(
		ctx, aid, req.Msg.GetKey(), req.Msg.GetContent(),
		int(req.Msg.GetMaxBytes()), int(req.Msg.GetMaxCount()),
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrAgentNoteInvalidKey):
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		case errors.Is(err, ErrAgentNoteTooLarge), errors.Is(err, ErrAgentNoteTooMany):
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		default:
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	return connect.NewResponse(&daemonv1.StateSaveNoteResponse{
		Note:      noteRowToProto(saved),
		Overwrote: overwrote,
	}), nil
}

func (h *agentStateHandler) DeleteNote(
	ctx context.Context, req *connect.Request[daemonv1.StateDeleteNoteRequest],
) (*connect.Response[daemonv1.StateDeleteNoteResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	existed, err := h.state.AgentNotesDelete(ctx, aid, req.Msg.GetKey())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&daemonv1.StateDeleteNoteResponse{Existed: existed}), nil
}

func (h *agentStateHandler) SetNoteInContext(
	ctx context.Context, req *connect.Request[daemonv1.StateSetNoteInContextRequest],
) (*connect.Response[daemonv1.StateSetNoteInContextResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	if setErr := h.state.AgentNotesSetInContext(ctx, aid, req.Msg.GetKey(), req.Msg.GetInContext()); setErr != nil {
		if errors.Is(setErr, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("note not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, setErr)
	}
	n, err := h.state.AgentNotesGet(ctx, aid, req.Msg.GetKey())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&daemonv1.StateSetNoteInContextResponse{
		Note: noteRowToProto(n),
	}), nil
}

func (h *agentStateHandler) CountNotes(
	ctx context.Context, _ *connect.Request[daemonv1.StateCountNotesRequest],
) (*connect.Response[daemonv1.StateCountNotesResponse], error) {
	aid, err := agentID(ctx)
	if err != nil {
		return nil, err
	}
	n, err := h.state.AgentNotesCount(ctx, aid)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&daemonv1.StateCountNotesResponse{Count: n}), nil
}

// ── proto ↔ Go conversions ──────────────────────────────────────

func messageRowToProto(m AgentMessageRow) *daemonv1.MessageRow {
	return &daemonv1.MessageRow{
		Id:           m.ID,
		SessionId:    m.SessionID,
		Role:         m.Role,
		Content:      m.Content,
		BranchesJson: m.BranchesJSON,
		ActiveBranch: m.ActiveBranch,
		CreatedUnix:  m.CreatedAt.Unix(),
	}
}

func messageRowFromProto(m *daemonv1.MessageRow) AgentMessageRow {
	if m == nil {
		return AgentMessageRow{}
	}
	return AgentMessageRow{
		ID:           m.GetId(),
		SessionID:    m.GetSessionId(),
		Role:         m.GetRole(),
		Content:      m.GetContent(),
		BranchesJSON: m.GetBranchesJson(),
		ActiveBranch: m.GetActiveBranch(),
	}
}

func noteRowToProto(n AgentNoteRow) *daemonv1.NoteRow {
	return &daemonv1.NoteRow{
		Key:         n.Key,
		Content:     n.Content,
		Preview:     n.Preview,
		InContext:   n.InContext,
		CreatedUnix: n.CreatedAt.Unix(),
		UpdatedUnix: n.UpdatedAt.Unix(),
	}
}
