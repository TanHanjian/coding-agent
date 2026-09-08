package sqlite_test

import (
	"context"
	"errors"
	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSQLiteQuestionRepositoryCreate(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(ctx, db.DB); err != nil {
		t.Fatal(err)
	}
	repo := sqlite.NewQuestionRepository(db)
	now := time.Now().UTC()
	record := question.QuestionRecord{ID: "q1", Title: "Title", Type: question.QuestionTypeAlgorithm, BodyMarkdown: "Body", Tags: []string{"go", "sql"}, CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	var title, created string
	var difficulty *string
	if err := db.QueryRowContext(ctx, `SELECT title, difficulty, created_at FROM questions WHERE id = ?`, record.ID).Scan(&title, &difficulty, &created); err != nil {
		t.Fatal(err)
	}
	if title != record.Title || difficulty != nil || created != now.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected row: %q %v %q", title, difficulty, created)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM question_tags WHERE question_id = ?`, record.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("got %d tags", count)
	}
	if err := repo.Create(ctx, record); err == nil {
		t.Fatal("expected duplicate ID error")
	}
	record.ID = "rollback"
	record.Tags = []string{"duplicate", "duplicate"}
	if err := repo.Create(ctx, record); err == nil {
		t.Fatal("expected duplicate tag error")
	}
	for _, query := range []string{`SELECT COUNT(*) FROM questions WHERE id = ?`, `SELECT COUNT(*) FROM question_tags WHERE question_id = ?`} {
		if err := db.QueryRowContext(ctx, query, record.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("transaction left %d rows", count)
		}
	}
	d := question.DifficultyHard
	record.ID, record.Tags, record.Difficulty = "hard", nil, &d
	if err := repo.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT difficulty FROM questions WHERE id = ?`, record.ID).Scan(&difficulty); err != nil {
		t.Fatal(err)
	}
	if difficulty == nil || *difficulty != string(d) {
		t.Fatalf("unexpected difficulty: %v", difficulty)
	}
}

func TestRepositoryUpdateTags(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tags      []string
		missing   bool
		wantError bool
		wantTags  []string
	}{
		{name: "replace", tags: []string{"new"}, wantTags: []string{"new"}},
		{name: "clear", tags: []string{}, wantTags: []string{}},
		{name: "nil keeps existing tags", wantTags: []string{"old"}},
		{name: "rollback", tags: []string{"new", "new"}, wantError: true, wantTags: []string{"old"}},
		{name: "missing", missing: true, wantError: true, wantTags: []string{"old"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "update.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := storage.Migrate(ctx, db.DB); err != nil {
				t.Fatal(err)
			}
			repo := sqlite.NewQuestionRepository(db)
			now := time.Now().UTC()
			q := question.QuestionRecord{ID: "q1", Title: "Original", Type: question.QuestionTypeKnowledge, BodyMarkdown: "Body", Tags: []string{"old"}, CreatedAt: now, UpdatedAt: now}
			if err := repo.Create(ctx, q); err != nil {
				t.Fatal(err)
			}
			q.Title, q.Tags = "Updated", tc.tags
			if tc.missing {
				q.ID = "missing"
			}
			err = repo.Update(ctx, q)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.missing && !errors.Is(err, question.ErrNotFound) {
				t.Fatalf("expected not found, got %v", err)
			}
			var title string
			if err := db.QueryRowContext(ctx, "SELECT title FROM questions WHERE id = 'q1'").Scan(&title); err != nil {
				t.Fatal(err)
			}
			wantTitle := "Updated"
			if tc.wantError {
				wantTitle = "Original"
			}
			if title != wantTitle {
				t.Fatalf("got title %q, want %q", title, wantTitle)
			}
			rows, err := db.QueryContext(ctx, "SELECT tag FROM question_tags WHERE question_id = 'q1' ORDER BY tag")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			tags := []string{}
			for rows.Next() {
				var tag string
				if err := rows.Scan(&tag); err != nil {
					t.Fatal(err)
				}
				tags = append(tags, tag)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tags, tc.wantTags) {
				t.Fatalf("got tags %v, want %v", tags, tc.wantTags)
			}
		})
	}
}
