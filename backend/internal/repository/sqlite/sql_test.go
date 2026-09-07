package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/question"
	"interview-memory-agent/backend/internal/repository/sqlite"
	"interview-memory-agent/backend/internal/storage"
)

func newSQLTestDB(t *testing.T) (*storage.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(ctx, db.DB); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, ctx
}

func createSQLQuestion(t *testing.T, ctx context.Context, db *storage.DB, id string, archived bool) {
	t.Helper()
	now := time.Now().UTC()
	if err := sqlite.NewQuestionRepository(db).Create(ctx, question.QuestionRecord{ID: id, Title: id + " title", Type: question.QuestionTypeAlgorithm, BodyMarkdown: "body", Tags: []string{"go", "sql"}, IsArchived: archived, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func TestSQLSearchExcludesArchivedAndLoadsTags(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLQuestion(t, ctx, db, "active", false)
	createSQLQuestion(t, ctx, db, "archived", true)
	result, err := sqlite.NewQuestionRepository(db).Search(ctx, question.QuestionSearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != "active" {
		t.Fatalf("unexpected search result: %+v", result)
	}
	if len(result.Items[0].Tags) != 2 {
		t.Fatalf("expected tags, got %v", result.Items[0].Tags)
	}
	result, err = sqlite.NewQuestionRepository(db).Search(ctx, question.QuestionSearchQuery{Archived: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Items[0].ID != "archived" {
		t.Fatalf("unexpected archived result: %+v", result)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestSQLAnswerCRUDAndDeleteCleanup(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLQuestion(t, ctx, db, "q1", false)
	now := time.Now().UTC()
	repo := sqlite.NewAnswerRepository(db)
	answer := question.AnswerAttempt{ID: "a1", QuestionID: "q1", BodyMarkdown: "answer", Result: question.AnswerResultCorrect, CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(ctx, answer); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if got.BodyMarkdown != answer.BodyMarkdown {
		t.Fatalf("unexpected answer: %+v", got)
	}
	items, err := repo.ListByQuestion(ctx, "q1")
	if err != nil || len(items) != 1 {
		t.Fatalf("list answers: %v, %v", err, items)
	}
	answer.BodyMarkdown = "updated"
	if err := repo.Update(ctx, answer); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(repo.Delete(ctx, "a1"), question.ErrNotFound) {
		t.Fatal("expected answer not found")
	}
}

func TestSQLReviewAssociationAndCRUD(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLQuestion(t, ctx, db, "q1", false)
	createSQLQuestion(t, ctx, db, "q2", false)
	now := time.Now().UTC()
	answerRepo := sqlite.NewAnswerRepository(db)
	if err := answerRepo.Create(ctx, question.AnswerAttempt{ID: "a1", QuestionID: "q1", Result: question.AnswerResultIncorrect, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	repo := sqlite.NewReviewRepository(db)
	review := question.MistakeReview{ID: "r1", QuestionID: "q1", AnswerAttemptID: stringPtr("a1"), ReviewMarkdown: "review", CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(ctx, review); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, question.MistakeReview{ID: "r2", QuestionID: "q2", AnswerAttemptID: stringPtr("a1"), CreatedAt: now, UpdatedAt: now}); !errors.Is(err, question.ErrInvalidInput) {
		t.Fatalf("expected invalid association, got %v", err)
	}
	if _, err := repo.GetByID(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	if items, err := repo.ListByQuestion(ctx, "q1"); err != nil || len(items) != 1 {
		t.Fatalf("list reviews: %v, %v", err, items)
	}
	if err := repo.Delete(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
}

func stringPtr(v string) *string { return &v }
