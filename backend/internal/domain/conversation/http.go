package conversation

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/transport/httpx"
)

func RegisterRoutes(r chi.Router, service Service) {
	r.Post("/conversations", CreateConversationHandler(service))
	r.Get("/conversations", ListConversationsHandler(service))
	r.Get("/conversations/{conversationID}", GetConversationHandler(service))
	r.Patch("/conversations/{conversationID}", UpdateConversationHandler(service))
	r.Delete("/conversations/{conversationID}", DeleteConversationHandler(service))
	r.Post("/conversations/{conversationID}/messages", CreateMessageHandler(service))
	r.Get("/conversations/{conversationID}/messages", ListMessagesHandler(service))
	r.Get("/messages/{messageID}", GetMessageHandler(service))
	r.Patch("/messages/{messageID}", UpdateAssistantMessageHandler(service))
	r.Delete("/messages/{messageID}", DeleteMessageHandler(service))
}

func CreateConversationHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input CreateConversationInput
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

func ListConversationsHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		records, err := service.ListConversations(r.Context())
		if err != nil {
			domainError(w, r, err, "读取会话失败")
			return
		}
		writeJSON(w, http.StatusOK, records)
	}
}

func GetConversationHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		record, err := service.GetConversation(r.Context(), chi.URLParam(r, "conversationID"))
		if err != nil {
			domainError(w, r, err, "读取会话失败")
			return
		}
		writeJSON(w, http.StatusOK, record)
	}
}

func UpdateConversationHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input UpdateConversationInput
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

func DeleteConversationHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := service.DeleteConversation(r.Context(), chi.URLParam(r, "conversationID")); err != nil {
			domainError(w, r, err, "删除会话失败")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func CreateMessageHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input CreateMessageInput
		if err := decodeJSON(r, &input); err != nil {
			invalid(w, r)
			return
		}
		record, err := service.CreateMessage(r.Context(), chi.URLParam(r, "conversationID"), input)
		if err != nil {
			domainError(w, r, err, "创建消息失败")
			return
		}
		writeJSON(w, http.StatusCreated, record)
	}
}

func ListMessagesHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		records, err := service.ListMessages(r.Context(), chi.URLParam(r, "conversationID"))
		if err != nil {
			domainError(w, r, err, "读取消息失败")
			return
		}
		writeJSON(w, http.StatusOK, records)
	}
}

func GetMessageHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		record, err := service.GetMessage(r.Context(), chi.URLParam(r, "messageID"))
		if err != nil {
			domainError(w, r, err, "读取消息失败")
			return
		}
		writeJSON(w, http.StatusOK, record)
	}
}

func UpdateAssistantMessageHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input UpdateAssistantMessageInput
		if err := decodeJSON(r, &input); err != nil {
			invalid(w, r)
			return
		}
		record, err := service.UpdateAssistantMessage(r.Context(), chi.URLParam(r, "messageID"), input)
		if err != nil {
			domainError(w, r, err, "更新助手消息失败")
			return
		}
		writeJSON(w, http.StatusOK, record)
	}
}

func DeleteMessageHandler(service Service) http.HandlerFunc {
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
