package httpchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestBudgetFailureSSELiveAndReconnectUseSafeCodeMapping(t *testing.T) {
	for _, tc := range []struct{ code, text string }{
		{chat.ContextBudgetExceededCode, "本次请求的上下文超过模型容量，生成已停止。请缩小请求范围，或使用上下文容量更大的模型。"},
		{chat.ContextCountingFailedCode, "本次请求的上下文无法安全计数，生成已停止。请检查模型和输入配置。"},
		{"model_failed", "生成失败，请重新生成"},
		{"private provider prompt API key", "生成失败，请重新生成"},
		{"", "生成失败，请重新生成"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			for _, reconnect := range []bool{false, true} {
				updates := make(chan chat.GenerationUpdate, 1)
				updates <- chat.GenerationUpdate{Kind: chat.GenerationUpdateTerminal, Status: conversation.MessageStatusFailed, ErrorCode: tc.code}
				close(updates)
				service := handlerService{
					start: func(context.Context, chat.StartInput) (chat.StartResult, error) {
						return chat.StartResult{ConversationID: "c1", AssistantMessageID: "a1"}, nil
					},
					subscribe: func(context.Context, string) (chat.GenerationSubscription, error) {
						snapshot := chat.GenerationSnapshot{AssistantMessageID: "a1", Content: "partial visible text", Status: conversation.MessageStatusStreaming}
						if reconnect {
							snapshot.Status, snapshot.ErrorCode = conversation.MessageStatusFailed, tc.code
						}
						return chat.GenerationSubscription{Snapshot: snapshot, Updates: updates}, nil
					},
				}
				router := chi.NewRouter()
				RegisterRoutes(router, service)
				request := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"id":"c1","message":{"id":"u1","role":"user","parts":[{"type":"text","text":"question"}]}}`))
				if reconnect {
					request = httptest.NewRequest(http.MethodGet, "/chat/a1/stream", nil)
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				body := response.Body.String()
				if response.Code != http.StatusOK || response.Header().Get(uiMessageStreamVersionHeader) != "v1" || !strings.Contains(body, "partial visible text") || !strings.HasSuffix(body, "data: [DONE]\n\n") {
					t.Fatalf("invalid stream (reconnect=%v): %d %s", reconnect, response.Code, body)
				}
				errors := 0
				for _, line := range strings.Split(body, "\n") {
					if !strings.HasPrefix(line, "data: {") {
						continue
					}
					var part map[string]any
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &part); err != nil {
						t.Fatal(err)
					}
					if part["type"] == "error" {
						errors++
						if part["errorText"] != tc.text || len(part) != 2 {
							t.Fatalf("unsafe/nonstandard error part: %+v", part)
						}
					}
					if part["type"] == "finish" || part["type"] == "abort" {
						t.Fatalf("failure emitted incorrect terminal: %+v", part)
					}
				}
				if errors != 1 || strings.Contains(body, "private") || strings.Contains(body, "API key") {
					t.Fatalf("incorrect/unsafe error stream: %s", body)
				}
				textEnd := strings.Index(body, `"type":"text-end"`)
				if textEnd < 0 || textEnd > strings.Index(body, `"type":"error"`) {
					t.Fatalf("error emitted before text closure: %s", body)
				}
			}
		})
	}
}
