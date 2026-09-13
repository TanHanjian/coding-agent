package interview

import (
	"context"
	"testing"
)

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
