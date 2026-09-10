// Package httpconversation 提供 Conversation 用例的 HTTP 传输适配器。
package httpconversation

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	conversation "interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/transport/httpx"
)

func RegisterRoutes(r chi.Router, service conversation.Service) {
	r.Post("/conversations", CreateConversationHandler(service))
	r.Get("/conversations", ListConversationsHandler(service))
	r.Get("/conversations/{conversationID}", GetConversationHandler(service))
	r.Patch("/conversations/{conversationID}", UpdateConversationHandler(service))
	r.Delete("/conversations/{conversationID}", DeleteConversationHandler(service))
	r.Get("/conversations/{conversationID}/messages", ListMessagesHandler(service))
	r.Get("/messages/{messageID}", GetMessageHandler(service))
	r.Delete("/messages/{messageID}", DeleteMessageHandler(service))
}

func CreateConversationHandler(service conversation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input conversation.CreateConversationInput
		if err := decodeJSON(r, &input); err != nil {
			invalid(w, r)
			return
		}
		record, err := service.CreateConversation(r.Context(), input)
		if err != nil {
			domainError(w, r, err, "创建会话失败")
			return
		}
		writeJSON(w, http.StatusCreated, record)
	}
}

func ListConversationsHandler(service conversation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		records, err := service.ListConversations(r.Context())
		if err != nil {
			domainError(w, r, err, "读取会话失败")
			return
		}
		writeJSON(w, http.StatusOK, records)
	}
}

func GetConversationHandler(service conversation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		record, err := service.GetConversation(r.Context(), chi.URLParam(r, "conversationID"))
		if err != nil {
			domainError(w, r, err, "读取会话失败")
			return
		}
		writeJSON(w, http.StatusOK, record)
	}
}

func UpdateConversationHandler(service conversation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input conversation.UpdateConversationInput
		if err := decodeJSON(r, &input); err != nil {
			invalid(w, r)
			return
		}
		record, err := service.UpdateConversation(r.Context(), chi.URLParam(r, "conversationID"), input)
		if err != nil {
			domainError(w, r, err, "更新会话失败")
			return
		}
		writeJSON(w, http.StatusOK, record)
	}
}

func DeleteConversationHandler(service conversation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := service.DeleteConversation(r.Context(), chi.URLParam(r, "conversationID")); err != nil {
			domainError(w, r, err, "删除会话失败")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func ListMessagesHandler(service conversation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		records, err := service.ListMessages(r.Context(), chi.URLParam(r, "conversationID"))
		if err != nil {
			domainError(w, r, err, "读取消息失败")
			return
		}
		writeJSON(w, http.StatusOK, records)
	}
}

func GetMessageHandler(service conversation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		record, err := service.GetMessage(r.Context(), chi.URLParam(r, "messageID"))
		if err != nil {
			domainError(w, r, err, "读取消息失败")
			return
		}
		writeJSON(w, http.StatusOK, record)
	}
}

func DeleteMessageHandler(service conversation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := service.DeleteMessage(r.Context(), chi.URLParam(r, "messageID")); err != nil {
			domainError(w, r, err, "删除消息失败")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeJSON(r *http.Request, target any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func invalid(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, "请求体无效")
}

func domainError(w http.ResponseWriter, r *http.Request, err error, message string) {
	if errors.Is(err, domainerr.ErrInvalidInput) {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, message)
	} else if errors.Is(err, domainerr.ErrNotFound) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, message)
	} else {
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, message)
	}
}
