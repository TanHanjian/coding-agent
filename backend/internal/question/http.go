package question

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"interview-memory-agent/backend/internal/httpx"
)

// RegisterQuestionRoutes 将题库相关 REST 路由注册到 chi 路由器。
func RegisterQuestionRoutes(r chi.Router, service QuestionService) {
	r.Route("/questions", func(r chi.Router) {
		r.Post("/", CreateQuestionHandler(service))
		r.Get("/", SearchQuestionsHandler(service))
		r.Get("/{questionID}", GetQuestionHandler(service))
		r.Patch("/{questionID}", UpdateQuestionHandler(service))
		r.Post("/{questionID}/archive", ArchiveQuestionHandler(service))
		r.Post("/{questionID}/restore", RestoreQuestionHandler(service))
		r.Delete("/{questionID}", DeleteQuestionHandler(service))
	})
}

// CreateQuestionHandler 处理创建题目的 HTTP 请求。
func CreateQuestionHandler(_ QuestionService) http.HandlerFunc { return notImplementedHandler }

// GetQuestionHandler 处理读取题目详情的 HTTP 请求。
func GetQuestionHandler(_ QuestionService) http.HandlerFunc { return notImplementedHandler }

// UpdateQuestionHandler 处理更新题目的 HTTP 请求。
func UpdateQuestionHandler(_ QuestionService) http.HandlerFunc { return notImplementedHandler }

// SearchQuestionsHandler 处理题库搜索和分页请求。
func SearchQuestionsHandler(_ QuestionService) http.HandlerFunc { return notImplementedHandler }

// ArchiveQuestionHandler 处理归档题目的 HTTP 请求。
func ArchiveQuestionHandler(_ QuestionService) http.HandlerFunc { return notImplementedHandler }

// RestoreQuestionHandler 处理恢复题目的 HTTP 请求。
func RestoreQuestionHandler(_ QuestionService) http.HandlerFunc { return notImplementedHandler }

// DeleteQuestionHandler 处理永久删除题目的 HTTP 请求。
func DeleteQuestionHandler(_ QuestionService) http.HandlerFunc { return notImplementedHandler }

// notImplementedHandler 统一返回尚未实现的功能响应，防止骨架误写业务数据。
func notImplementedHandler(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotImplemented, "not_implemented", "题库功能尚未实现")
}
