// Package httpchat 提供 Chat 用例的 HTTP 与 SSE 传输适配器。
package httpchat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/transport/httpx"

	"github.com/go-chi/chi/v5"
)

// RegisterRoutes 注册聊天入口。Handler 只负责 HTTP 请求校验、订阅和 AI SDK
// UI Message Stream 编码；生成生命周期仍由 chat.Service 管理。
func RegisterRoutes(r chi.Router, service chat.Service) {
	r.Post("/chat", StartHandler(service))
	r.Get("/chat/{assistantMessageID}/stream", ResumeStreamHandler(service))
	r.Post("/chat/{assistantMessageID}/cancel", CancelHandler(service))
}

type startRequest struct {
	ID      string    `json:"id"`
	Message uiMessage `json:"message"`
}

type uiMessage struct {
	ID    string        `json:"id"`
	Role  string        `json:"role"`
	Parts []messagePart `json:"parts"`
}

type messagePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func StartHandler(service chat.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		input, err := decodeStartRequest(r)
		if err != nil {
			writeChatError(w, r, err, "聊天请求无效")
			return
		}

		result, err := service.Start(r.Context(), input)
		if err != nil {
			writeChatError(w, r, err, "暂时无法开始生成")
			return
		}
		sub, err := service.Subscribe(r.Context(), result.AssistantMessageID)
		if err != nil {
			writeChatError(w, r, err, "暂时无法订阅生成")
			return
		}
		defer sub.Close()

		w.Header().Set("X-Conversation-ID", result.ConversationID)
		w.Header().Set("X-User-Message-ID", result.UserMessageID)
		w.Header().Set("X-Assistant-Message-ID", result.AssistantMessageID)
		streamSubscription(w, r, sub)
	}
}

func ResumeStreamHandler(service chat.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subscription, err := service.Subscribe(r.Context(), chi.URLParam(r, "assistantMessageID"))
		if err != nil {
			writeChatError(w, r, err, "暂时无法恢复生成")
			return
		}
		defer subscription.Close()

		w.Header().Set("X-Assistant-Message-ID", subscription.Snapshot.AssistantMessageID)
		streamSubscription(w, r, subscription)
	}
}

const (
	uiMessageStreamVersionHeader = "x-vercel-ai-ui-message-stream"
	uiMessageStreamVersion       = "v1"
	streamUnavailableErrorText   = "生成连接已中断，请重新生成"
	streamFailedErrorText        = "生成失败，请重新生成"
)

// streamSubscription 将 Chat 应用层的 snapshot + delta 订阅编码为 AI SDK UI
// Message Stream v1。它只结束浏览器订阅；r.Context() 取消不会传递给后台生成。
func streamSubscription(w http.ResponseWriter, r *http.Request, subscription chat.GenerationSubscription) {
	stream := uiMessageStreamWriter{response: w}
	stream.setHeaders()

	textPartID := subscription.Snapshot.AssistantMessageID + "-text"
	if !stream.writePart(map[string]string{
		"type":      "start",
		"messageId": subscription.Snapshot.AssistantMessageID,
	}) || !stream.writePart(map[string]string{
		"type": "text-start",
		"id":   textPartID,
	}) {
		return
	}
	if subscription.Snapshot.Content != "" && !stream.writePart(map[string]string{
		"type":  "text-delta",
		"id":    textPartID,
		"delta": subscription.Snapshot.Content,
	}) {
		return
	}

	if subscription.Snapshot.Status != conversation.MessageStatusStreaming {
		stream.writeTerminal(textPartID, subscription.Snapshot.Status)
		return
	}
	if subscription.Updates == nil {
		stream.writeError(textPartID, streamUnavailableErrorText)
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case update, ok := <-subscription.Updates:
			if !ok {
				stream.writeError(textPartID, streamUnavailableErrorText)
				return
			}
			switch update.Kind {
			case chat.GenerationUpdateDelta:
				if !stream.writePart(map[string]string{
					"type":  "text-delta",
					"id":    textPartID,
					"delta": update.Text,
				}) {
					return
				}
			case chat.GenerationUpdateTerminal:
				stream.writeTerminal(textPartID, update.Status)
				return
			default:
				stream.writeError(textPartID, streamFailedErrorText)
				return
			}
		}
	}
}

type uiMessageStreamWriter struct {
	response http.ResponseWriter
}

func (w uiMessageStreamWriter) setHeaders() {
	w.response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.response.Header().Set("Cache-Control", "no-cache, no-transform")
	w.response.Header().Set(uiMessageStreamVersionHeader, uiMessageStreamVersion)
	w.response.Header().Set("X-Accel-Buffering", "no")
}

func (w uiMessageStreamWriter) writePart(part any) bool {
	payload, err := json.Marshal(part)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w.response, "data: %s\n\n", payload); err != nil {
		return false
	}
	w.flush()
	return true
}

func (w uiMessageStreamWriter) writeDone() bool {
	if _, err := fmt.Fprint(w.response, "data: [DONE]\n\n"); err != nil {
		return false
	}
	w.flush()
	return true
}

func (w uiMessageStreamWriter) writeTerminal(textPartID string, status conversation.MessageStatus) {
	if !w.writePart(map[string]string{"type": "text-end", "id": textPartID}) {
		return
	}
	switch status {
	case conversation.MessageStatusCompleted:
		if !w.writePart(map[string]string{"type": "finish"}) {
			return
		}
	case conversation.MessageStatusCancelled:
		if !w.writePart(map[string]string{"type": "abort", "reason": "user cancelled"}) {
			return
		}
	default:
		if !w.writePart(map[string]string{"type": "error", "errorText": streamFailedErrorText}) {
			return
		}
	}
	w.writeDone()
}

func (w uiMessageStreamWriter) writeError(textPartID, message string) {
	if !w.writePart(map[string]string{"type": "text-end", "id": textPartID}) {
		return
	}
	if !w.writePart(map[string]string{"type": "error", "errorText": message}) {
		return
	}
	w.writeDone()
}

func (w uiMessageStreamWriter) flush() {
	if flusher, ok := w.response.(http.Flusher); ok {
		flusher.Flush()
	}
}

func CancelHandler(service chat.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		record, err := service.Cancel(r.Context(), chi.URLParam(r, "assistantMessageID"))
		if err != nil {
			writeChatError(w, r, err, "暂时无法取消生成")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(record)
	}
}

func decodeStartRequest(r *http.Request) (chat.StartInput, error) {
	var request startRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return chat.StartInput{}, domainerr.ErrInvalidInput
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return chat.StartInput{}, domainerr.ErrInvalidInput
	}
	if strings.TrimSpace(request.ID) == "" || strings.TrimSpace(request.Message.ID) == "" || request.Message.Role != "user" || len(request.Message.Parts) != 1 || request.Message.Parts[0].Type != "text" || strings.TrimSpace(request.Message.Parts[0].Text) == "" {
		return chat.StartInput{}, domainerr.ErrInvalidInput
	}
	return chat.StartInput{ConversationID: request.ID, ClientMessageID: request.Message.ID, Text: request.Message.Parts[0].Text}, nil
}

func writeChatError(w http.ResponseWriter, r *http.Request, err error, message string) {
	switch {
	case errors.Is(err, domainerr.ErrInvalidInput):
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, message)
	case errors.Is(err, domainerr.ErrNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, message)
	case errors.Is(err, domainerr.ErrConflict):
		httpx.WriteError(w, r, http.StatusConflict, "generation_active", "当前会话已有正在生成的回答")
	case errors.Is(err, domainerr.ErrNotImplemented):
		httpx.WriteError(w, r, http.StatusNotImplemented, "not_implemented", "聊天生成尚未实现")
	default:
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, message)
	}
}
