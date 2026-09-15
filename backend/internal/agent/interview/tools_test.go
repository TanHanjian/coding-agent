package interview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"interview-memory-agent/backend/internal/domain/question"
)

func TestSearchQuestionMemoryToolUsesBoundedDomainSearch(t *testing.T) {
	difficulty := question.DifficultyMedium
	searcher := &questionSearcherStub{result: question.QuestionSearchResult{
		Items: []question.QuestionRecord{{
			ID:         "q-1",
			Title:      "二叉树遍历",
			Type:       question.QuestionTypeAlgorithm,
			Difficulty: &difficulty,
			Tags:       []string{"tree"},
		}},
		Total:   7,
		HasNext: true,
	}}
	tool, err := NewSearchQuestionMemoryTool(searcher)
	if err != nil {
		t.Fatalf("NewSearchQuestionMemoryTool() error = %v", err)
	}

	result, err := tool.InvokableRun(context.Background(), `{"query":"二叉树","tags":["tree"],"limit":2}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if searcher.query.Text != "二叉树" || searcher.query.Page != 1 || searcher.query.PageSize != 2 {
		t.Fatalf("search query = %#v, want text and first bounded page", searcher.query)
	}
	if got, want := searcher.query.Tags, []string{"tree"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("search tags = %#v, want %#v", got, want)
	}
	if !strings.Contains(result, `"id":"q-1"`) || !strings.Contains(result, `"hasMore":true`) {
		t.Fatalf("tool result = %s, want compact result payload", result)
	}
}

func TestSearchQuestionMemoryToolRejectsUnboundedOrEmptySearch(t *testing.T) {
	searcher := &questionSearcherStub{}
	tool, err := NewSearchQuestionMemoryTool(searcher)
	if err != nil {
		t.Fatalf("NewSearchQuestionMemoryTool() error = %v", err)
	}

	for _, input := range []string{`{}`, `{"query":"Go","limit":6}`} {
		if _, err := tool.InvokableRun(context.Background(), input); err == nil {
			t.Fatalf("InvokableRun(%s) error = nil, want validation error", input)
		}
	}
	if searcher.calls != 0 {
		t.Fatalf("searcher calls = %d, want 0 for invalid input", searcher.calls)
	}
}

func TestSearchQuestionMemoryToolReturnsSearcherError(t *testing.T) {
	searcher := &questionSearcherStub{err: errors.New("storage unavailable")}
	tool, err := NewSearchQuestionMemoryTool(searcher)
	if err != nil {
		t.Fatalf("NewSearchQuestionMemoryTool() error = %v", err)
	}
	if _, err := tool.InvokableRun(context.Background(), `{"query":"Go"}`); !errors.Is(err, searcher.err) {
		t.Fatalf("InvokableRun() error = %v, want searcher error", err)
	}
}

func TestNewToolsBuildsSearchQuestionMemoryTool(t *testing.T) {
	tools, err := NewTools(ToolDependencies{
		QuestionSearcher:      &questionSearcherStub{},
		QuestionContextReader: &questionContextReaderStub{},
	})
	if err != nil {
		t.Fatalf("NewTools() error = %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tool count = %d, want 2", len(tools))
	}
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		info, err := tool.Info(context.Background())
		if err != nil {
			t.Fatalf("tool.Info() error = %v", err)
		}
		names[info.Name] = true
	}
	for _, want := range []string{"search_question_memory", "get_question_context"} {
		if !names[want] {
			t.Fatalf("tool names = %#v, missing %q", names, want)
		}
	}
}

func TestGetQuestionContextToolReturnsBoundedDomainDetail(t *testing.T) {
	difficulty := question.DifficultyHard
	duration := int64(1250)
	answerAttemptID := "a-1"
	reader := &questionContextReaderStub{detail: question.QuestionDetail{
		Question: question.QuestionRecord{
			ID:           "q-1",
			Title:        "二叉树遍历",
			Type:         question.QuestionTypeAlgorithm,
			Difficulty:   &difficulty,
			Tags:         []string{"tree"},
			BodyMarkdown: strings.Repeat("题", maxQuestionBodyRunes+1),
			SourceName:   "题库",
		},
		Answers: []question.AnswerAttempt{
			{ID: "a-1", Result: question.AnswerResultPartial, BodyMarkdown: strings.Repeat("答", maxAnswerBodyRunes+1), Code: strings.Repeat("c", maxAnswerCodeRunes+1), CodeLanguage: "go", DurationMs: &duration},
			{ID: "a-2", Result: question.AnswerResultIncorrect, BodyMarkdown: "第二次作答"},
			{ID: "a-3", Result: question.AnswerResultCorrect, BodyMarkdown: "第三次作答"},
			{ID: "a-4", Result: question.AnswerResultCorrect, BodyMarkdown: "不应返回"},
		},
		Reviews: []question.MistakeReview{
			{ID: "r-1", AnswerAttemptID: &answerAttemptID, ReviewMarkdown: strings.Repeat("复", maxReviewBodyRunes+1), CorrectionMarkdown: "修正", KeyConclusions: "结论"},
			{ID: "r-2", ReviewMarkdown: "第二条"},
			{ID: "r-3", ReviewMarkdown: "第三条"},
			{ID: "r-4", ReviewMarkdown: "不应返回"},
		},
	}}
	for index := 0; index < maxQuestionContextAttachments+1; index++ {
		reader.detail.Attachments = append(reader.detail.Attachments, question.Attachment{
			ID:           "file-" + string(rune('a'+index)),
			OriginalName: "notes.md",
			MIMEType:     "text/markdown",
			SizeBytes:    64,
		})
	}

	tool, err := NewGetQuestionContextTool(reader)
	if err != nil {
		t.Fatalf("NewGetQuestionContextTool() error = %v", err)
	}
	payload, err := tool.InvokableRun(context.Background(), `{"questionId":" q-1 "}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if reader.questionID != "q-1" || reader.calls != 1 {
		t.Fatalf("reader call = id %q, count %d; want q-1 once", reader.questionID, reader.calls)
	}

	var result GetQuestionContextResult
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.Found || result.Question.ID != "q-1" || result.Question.Difficulty != "hard" {
		t.Fatalf("question result = %#v, want found compact question", result.Question)
	}
	if !strings.HasSuffix(result.Question.BodyMarkdown, truncatedTextSuffix) {
		t.Fatalf("question body = %q, want truncation suffix", result.Question.BodyMarkdown)
	}
	if len(result.Answers) != maxQuestionContextAnswers || !result.AnswersTruncated {
		t.Fatalf("answers = %#v, truncated = %v", result.Answers, result.AnswersTruncated)
	}
	if result.Answers[0].ID != "a-1" || !strings.HasSuffix(result.Answers[0].Code, truncatedTextSuffix) {
		t.Fatalf("first answer = %#v, want bounded source answer", result.Answers[0])
	}
	if len(result.Reviews) != maxQuestionContextReviews || !result.ReviewsTruncated || result.Reviews[0].AnswerAttemptID != "a-1" {
		t.Fatalf("reviews = %#v, truncated = %v", result.Reviews, result.ReviewsTruncated)
	}
	if len(result.Attachments) != maxQuestionContextAttachments || !result.AttachmentsTruncated || result.Attachments[0].ID != "file-a" {
		t.Fatalf("attachments = %#v, truncated = %v", result.Attachments, result.AttachmentsTruncated)
	}
}

func TestGetQuestionContextToolHandlesMissingInvalidAndInternalFailures(t *testing.T) {
	notFoundReader := &questionContextReaderStub{err: question.ErrNotFound}
	tool, err := NewGetQuestionContextTool(notFoundReader)
	if err != nil {
		t.Fatalf("NewGetQuestionContextTool() error = %v", err)
	}
	payload, err := tool.InvokableRun(context.Background(), `{"questionId":"missing"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	var result GetQuestionContextResult
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Found {
		t.Fatalf("not found result = %#v, want found=false", result)
	}

	if _, err := tool.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("InvokableRun() error = nil, want required questionId error")
	}
	if notFoundReader.calls != 1 {
		t.Fatalf("reader calls = %d, want 1 after invalid input", notFoundReader.calls)
	}

	internalReader := &questionContextReaderStub{err: errors.New("sqlite: locked")}
	internalTool, err := NewGetQuestionContextTool(internalReader)
	if err != nil {
		t.Fatalf("NewGetQuestionContextTool() error = %v", err)
	}
	if _, err := internalTool.InvokableRun(context.Background(), `{"questionId":"q-1"}`); err == nil || strings.Contains(err.Error(), "sqlite") {
		t.Fatalf("InvokableRun() error = %v, want sanitized lookup error", err)
	}
}

func TestFakeSearchQuestionsTool(t *testing.T) {
	tool, err := NewFakeSearchQuestionsTool()
	if err != nil {
		t.Fatal(err)
	}
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "search_questions" {
		t.Fatalf("name=%q", info.Name)
	}
	result, err := tool.InvokableRun(context.Background(), `{"query":"二叉树"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("empty result")
	}
}

type questionSearcherStub struct {
	query  question.QuestionSearchQuery
	result question.QuestionSearchResult
	err    error
	calls  int
}

type questionContextReaderStub struct {
	detail     question.QuestionDetail
	err        error
	questionID string
	calls      int
}

func (s *questionContextReaderStub) Get(_ context.Context, questionID string) (question.QuestionDetail, error) {
	s.calls++
	s.questionID = questionID
	return s.detail, s.err
}

func (s *questionSearcherStub) Search(_ context.Context, query question.QuestionSearchQuery) (question.QuestionSearchResult, error) {
	s.calls++
	s.query = query
	return s.result, s.err
}
