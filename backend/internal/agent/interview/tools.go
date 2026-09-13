package interview

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

type FakeSearchInput struct {
	Query string `json:"query" jsonschema_description:"Question search text"`
}

type FakeSearchResult struct {
	Items []FakeSearchItem `json:"items"`
}

type FakeSearchItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// NewFakeSearchQuestionsTool is a deterministic fixture for Tool Calling tests.
func NewFakeSearchQuestionsTool() (tool.InvokableTool, error) {
	return toolutils.InferTool[FakeSearchInput, FakeSearchResult](
		"search_questions",
		"Search interview questions by text (development fixture).",
		func(_ context.Context, in FakeSearchInput) (FakeSearchResult, error) {
			if in.Query == "" {
				return FakeSearchResult{Items: []FakeSearchItem{}}, nil
			}
			return FakeSearchResult{Items: []FakeSearchItem{{ID: "fake-q-1", Title: "Fixture: " + in.Query}}}, nil
		},
	)
}
