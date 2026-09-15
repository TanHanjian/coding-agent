// Package hot100 provides a deliberately small, repeatable catalog import for
// the user's own LeetCode Hot 100 practice records.
package hot100

import (
	"context"
	"fmt"
	"strings"

	"interview-memory-agent/backend/internal/domain/question"
)

const sourceName = "LeetCode"

// Catalog is the narrow domain seam needed by the importer. It keeps the
// command independent from SQLite while preserving validation in question.Service.
type Catalog interface {
	Create(context.Context, question.CreateQuestionInput) (question.QuestionRecord, error)
	Search(context.Context, question.QuestionSearchQuery) (question.QuestionSearchResult, error)
}

type Importer struct{ catalog Catalog }

type Result struct {
	Total   int
	Created int
	Skipped int
}

func NewImporter(catalog Catalog) (*Importer, error) {
	if catalog == nil {
		return nil, fmt.Errorf("hot100 importer: catalog is required")
	}
	return &Importer{catalog: catalog}, nil
}

// Import adds only entries whose canonical LeetCode URL is absent. It never
// changes existing user records, including ones created by an earlier import.
func (i *Importer) Import(ctx context.Context, dryRun bool) (Result, error) {
	existing, err := i.existingURLs(ctx)
	if err != nil {
		return Result{}, err
	}
	result := Result{Total: len(Manifest)}
	for _, entry := range Manifest {
		input := entry.input()
		if _, ok := existing[input.SourceURL]; ok {
			result.Skipped++
			continue
		}
		if !dryRun {
			if _, err := i.catalog.Create(ctx, input); err != nil {
				return result, fmt.Errorf("create %q: %w", entry.Title, err)
			}
		}
		existing[input.SourceURL] = struct{}{}
		result.Created++
	}
	return result, nil
}

func (i *Importer) existingURLs(ctx context.Context) (map[string]struct{}, error) {
	urls := make(map[string]struct{})
	for page := 1; ; page++ {
		found, err := i.catalog.Search(ctx, question.QuestionSearchQuery{
			Source: sourceName, Page: page, PageSize: 100, SortBy: "createdAt", SortDirection: "asc",
		})
		if err != nil {
			return nil, fmt.Errorf("search existing LeetCode questions: %w", err)
		}
		for _, item := range found.Items {
			if url := strings.TrimSpace(item.SourceURL); url != "" {
				urls[url] = struct{}{}
			}
		}
		if !found.HasNext {
			return urls, nil
		}
	}
}

type Entry struct {
	Number     int
	Slug       string
	Title      string
	Difficulty question.Difficulty
	Category   string
}

func (e Entry) input() question.CreateQuestionInput {
	difficulty := e.Difficulty
	return question.CreateQuestionInput{
		Title: e.Title, Type: question.QuestionTypeAlgorithm, Difficulty: &difficulty,
		SourceName: sourceName, SourceURL: "https://leetcode.cn/problems/" + e.Slug + "/",
		Tags:         []string{"leetcode", "hot-100", e.Category},
		BodyMarkdown: "# LeetCode Hot 100 练习\n\n完整题面请在原题链接查看。\n\n在这里记录自己的题意摘要、解法与复杂度、易错点和复盘。\n",
	}
}
