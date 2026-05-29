package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"taffy-server/internal/middleware"
	"taffy-server/internal/model"
	"taffy-server/internal/service"
)

// ConversationHandler 对话历史相关 HTTP 处理器。
type ConversationHandler struct {
	convSvc *service.ConversationService
}

func NewConversationHandler(convSvc *service.ConversationService) *ConversationHandler {
	return &ConversationHandler{convSvc: convSvc}
}

// ListConversations GET /api/v1/conversations
func (h *ConversationHandler) ListConversations(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	convs, err := h.convSvc.ListConversations(r.Context(), userID)
	if err != nil {
		slog.Error("list conversations failed", "err", err)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	if convs == nil {
		convs = []service.ConversationWithPreview{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(convs)
}

// GetMessages GET /api/v1/conversations/{id}/messages
func (h *ConversationHandler) GetMessages(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	idStr := r.PathValue("id")
	convID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || convID <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_conversation_id", "invalid conversation id")
		return
	}

	// 校验会话归属（用户只能看自己的对话）
	conv, err := h.convSvc.GetConversation(r.Context(), convID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "conversation_not_found", "conversation not found")
		return
	}
	if conv.UserID != userID {
		writeAPIError(w, http.StatusForbidden, "not_owner", "not conversation owner")
		return
	}

	msgs, err := h.convSvc.GetMessages(r.Context(), convID)
	if err != nil {
		slog.Error("get messages failed", "err", err, "conversation_id", convID)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	if msgs == nil {
		msgs = []*model.Message{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msgs)
}
