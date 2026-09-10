// Package httpchat 提供 Chat 用例的 HTTP 与 SSE 传输适配器。
package httpchat

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/transport/httpx"
)

// RegisterRoutes 注册聊天入口。当前 SkeletonService 返回 501；路由、请求形状与
// 错误边界在 Agent/SQLite 逻辑实现前就已固定。
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
		// 实现后在这里订阅 result.AssistantMessageID 并编码 UI Message Data Stream；
		// 浏览器刷新使用下方 GET 路由订阅同一条运行。
		_ = result
	}
}

func ResumeStreamHandler(service chat.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO: 调用 service.Subscribe。先把 subscription.Snapshot 编码为“替换
		// 助手全文”的 UI Message 事件，再编码 subscription.Updates 的 delta/terminal。
		// 当前禁止用自定义 SSE 冒充该协议，因此骨架明确返回 501。
		writeChatError(w, r, domainerr.ErrNotImplemented, "流式恢复尚未实现")
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
