package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func recordORMTime() time.Time {
	return time.Date(2026, 9, 14, 10, 11, 12, 123456789, time.UTC)
}

// Fixtures are written independently of repository models and ORM-generated SQL.
func seedRecordORMQuestion(t *testing.T, db *storage.DB, ctx context.Context, id string) {
	t.Helper()
	at := recordORMTime().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `INSERT INTO questions
		(id, title, type, body_markdown, difficulty, source_name, source_url, is_archived, created_at, updated_at)
		VALUES (?, 'fixture', 'algorithm', '', NULL, '', '', 0, ?, ?)`, id, at, at); err != nil {
		t.Fatal(err)
	}
}

func readRecordORMAnswerSQL(t *testing.T, db *storage.DB, ctx context.Context, id string) question.AnswerAttempt {
	t.Helper()
	var record question.AnswerAttempt
	var createdAt, updatedAt string
	if err := db.QueryRowContext(ctx, `SELECT id, question_id, body_markdown, code, code_language,
		result, duration_ms, created_at, updated_at FROM answer_attempts WHERE id = ?`, id).
		Scan(&record.ID, &record.QuestionID, &record.BodyMarkdown, &record.Code, &record.CodeLanguage,
			&record.Result, &record.DurationMs, &createdAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	record.CreatedAt = parseRecordORMTime(t, createdAt)
	record.UpdatedAt = parseRecordORMTime(t, updatedAt)
	return record
}

func readRecordORMReviewSQL(t *testing.T, db *storage.DB, ctx context.Context, id string) question.MistakeReview {
	t.Helper()
	var record question.MistakeReview
	var createdAt, updatedAt string
	if err := db.QueryRowContext(ctx, `SELECT id, question_id, answer_attempt_id, mistake_category,
		review_markdown, correction_markdown, key_conclusions, ai_content_markdown, ai_source,
		created_at, updated_at FROM mistake_reviews WHERE id = ?`, id).
		Scan(&record.ID, &record.QuestionID, &record.AnswerAttemptID, &record.MistakeCategory,
			&record.ReviewMarkdown, &record.CorrectionMarkdown, &record.KeyConclusions,
			&record.AIContentMarkdown, &record.AISource, &createdAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	record.CreatedAt = parseRecordORMTime(t, createdAt)
	record.UpdatedAt = parseRecordORMTime(t, updatedAt)
	return record
}

func readRecordORMAttachmentSQL(t *testing.T, db *storage.DB, ctx context.Context, id string) question.Attachment {
	t.Helper()
	var record question.Attachment
	var createdAt string
	if err := db.QueryRowContext(ctx, `SELECT id, question_id, owner_type, owner_id, original_name,
		stored_name, mime_type, size_bytes, sha256, created_at FROM attachments WHERE id = ?`, id).
		Scan(&record.ID, &record.QuestionID, &record.OwnerType, &record.OwnerID, &record.OriginalName,
			&record.StoredName, &record.MIMEType, &record.SizeBytes, &record.SHA256, &createdAt); err != nil {
		t.Fatal(err)
	}
	record.CreatedAt = parseRecordORMTime(t, createdAt)
	return record
}

func parseRecordORMTime(t *testing.T, text string) time.Time {
	t.Helper()
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRecordORMLegacyFixtureAndRoundTrip(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	seedRecordORMQuestion(t, db, ctx, "q1")
	at := recordORMTime().Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO answer_attempts (id, question_id, body_markdown, code, code_language, result, duration_ms, created_at, updated_at)
		 VALUES ('legacy-answer', 'q1', 'body', 'code', 'go', 'partial', 0, ?, ?)`,
		`INSERT INTO answer_attempts (id, question_id, body_markdown, code, code_language, result, duration_ms, created_at, updated_at)
		 VALUES ('legacy-null-answer', 'q1', '', '', '', 'skipped', NULL, ?, ?)`,
		`INSERT INTO mistake_reviews (id, question_id, answer_attempt_id, mistake_category, review_markdown,
		 correction_markdown, key_conclusions, ai_content_markdown, ai_source, created_at, updated_at)
		 VALUES ('legacy-review', 'q1', 'legacy-answer', 'category', 'review', 'correction', 'conclusion', 'ai', 'source', ?, ?)`,
		`INSERT INTO mistake_reviews (id, question_id, answer_attempt_id, mistake_category, review_markdown,
		 correction_markdown, key_conclusions, ai_content_markdown, ai_source, created_at, updated_at)
		 VALUES ('legacy-null-review', 'q1', NULL, '', '', '', '', '', '', ?, ?)`,
	} {
		if _, err := db.ExecContext(ctx, statement, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO attachments (id, question_id, owner_type, owner_id,
		original_name, stored_name, mime_type, size_bytes, sha256, created_at)
		VALUES ('legacy-attachment', 'q1', 'answer', 'legacy-answer', 'old.txt', 'stored.txt', 'text/plain', 0, 'hash', ?)`, at); err != nil {
		t.Fatal(err)
	}
	answers := sqlite.NewAnswerRepository(db)
	reviews := sqlite.NewReviewRepository(db)
	attachments := sqlite.NewAttachmentRepository(db)
	for _, id := range []string{"legacy-answer", "legacy-null-answer"} {
		got, err := answers.GetByID(ctx, id)
		want := readRecordORMAnswerSQL(t, db, ctx, id)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("legacy answer: got %+v, want %+v, err %v", got, want, err)
		}
	}
	for _, id := range []string{"legacy-review", "legacy-null-review"} {
		got, err := reviews.GetByID(ctx, id)
		want := readRecordORMReviewSQL(t, db, ctx, id)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("legacy review: got %+v, want %+v, err %v", got, want, err)
		}
	}
	gotAttachment, err := attachments.GetByID(ctx, "legacy-attachment")
	if want := readRecordORMAttachmentSQL(t, db, ctx, "legacy-attachment"); err != nil || !reflect.DeepEqual(gotAttachment, want) {
		t.Fatalf("legacy attachment: got %+v, want %+v, err %v", gotAttachment, want, err)
	}

	// A non-UTC caller clock must be persisted in the original UTC text format.
	localTime := recordORMTime().In(time.FixedZone("caller", 8*60*60))
	duration := int64(123)
	answer := question.AnswerAttempt{ID: "new-answer", QuestionID: "q1", BodyMarkdown: "new body",
		Code: "new code", CodeLanguage: "go", Result: question.AnswerResultCorrect, DurationMs: &duration,
		CreatedAt: localTime, UpdatedAt: localTime}
	if err := answers.Create(ctx, answer); err != nil {
		t.Fatal(err)
	}
	answer.CreatedAt, answer.UpdatedAt = recordORMTime(), recordORMTime()
	if got := readRecordORMAnswerSQL(t, db, ctx, answer.ID); !reflect.DeepEqual(got, answer) {
		t.Fatalf("new answer SQL round trip: got %+v, want %+v", got, answer)
	}
	if got, err := answers.GetByID(ctx, answer.ID); err != nil || !reflect.DeepEqual(got, answer) {
		t.Fatalf("new answer ORM round trip: got %+v, want %+v, err %v", got, answer, err)
	}
	review := question.MistakeReview{ID: "new-review", QuestionID: "q1", AnswerAttemptID: &answer.ID,
		MistakeCategory: "new category", ReviewMarkdown: "new review", CorrectionMarkdown: "fix",
		KeyConclusions: "key", AIContentMarkdown: "content", AISource: "source", CreatedAt: localTime, UpdatedAt: localTime}
	if err := reviews.Create(ctx, review); err != nil {
		t.Fatal(err)
	}
	review.CreatedAt, review.UpdatedAt = recordORMTime(), recordORMTime()
	if got := readRecordORMReviewSQL(t, db, ctx, review.ID); !reflect.DeepEqual(got, review) {
		t.Fatalf("new review SQL round trip: got %+v, want %+v", got, review)
	}
	if got, err := reviews.GetByID(ctx, review.ID); err != nil || !reflect.DeepEqual(got, review) {
		t.Fatalf("new review ORM round trip: got %+v, want %+v, err %v", got, review, err)
	}
	attachment := question.Attachment{ID: "new-attachment", QuestionID: "q1", OwnerType: "review", OwnerID: review.ID,
		OriginalName: "new.txt", StoredName: "new-stored.txt", MIMEType: "text/plain", SizeBytes: 123,
		SHA256: "new hash", CreatedAt: localTime}
	if err := attachments.Create(ctx, attachment); err != nil {
		t.Fatal(err)
	}
	attachment.CreatedAt = recordORMTime()
	if got := readRecordORMAttachmentSQL(t, db, ctx, attachment.ID); !reflect.DeepEqual(got, attachment) {
		t.Fatalf("new attachment SQL round trip: got %+v, want %+v", got, attachment)
	}
	if got, err := attachments.GetByID(ctx, attachment.ID); err != nil || !reflect.DeepEqual(got, attachment) {
		t.Fatalf("new attachment ORM round trip: got %+v, want %+v, err %v", got, attachment, err)
	}
	var storedAt, storageType string
	if err := db.QueryRowContext(ctx, `SELECT created_at, typeof(created_at) FROM answer_attempts WHERE id = ?`, answer.ID).
		Scan(&storedAt, &storageType); err != nil || storedAt != at || storageType != "text" {
		t.Fatalf("caller time replaced or format changed: %q %q, err %v", storedAt, storageType, err)
	}
}

func TestRecordORMZeroNullAndMissing(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	seedRecordORMQuestion(t, db, ctx, "q1")
	seedRecordORMQuestion(t, db, ctx, "q2")
	answers, reviews, attachments := sqlite.NewAnswerRepository(db), sqlite.NewReviewRepository(db), sqlite.NewAttachmentRepository(db)
	duration := int64(99)
	answer := question.AnswerAttempt{ID: "a1", QuestionID: "q1", BodyMarkdown: "body", Code: "code", CodeLanguage: "go",
		Result: question.AnswerResultCorrect, DurationMs: &duration, CreatedAt: recordORMTime(), UpdatedAt: recordORMTime()}
	if err := answers.Create(ctx, answer); err != nil {
		t.Fatal(err)
	}
	update := question.AnswerAttempt{ID: "a1", QuestionID: "q2", Result: question.AnswerResultSkipped}
	zero := int64(0)
	for _, value := range []*int64{&zero, nil} {
		update.DurationMs = value
		if err := answers.Update(ctx, update); err != nil {
			t.Fatal(err)
		}
		got := readRecordORMAnswerSQL(t, db, ctx, "a1")
		if got.QuestionID != "q1" || got.BodyMarkdown != "" || got.Code != "" || got.CodeLanguage != "" ||
			got.Result != question.AnswerResultSkipped || !reflect.DeepEqual(got.DurationMs, value) ||
			!got.CreatedAt.Equal(recordORMTime()) || !got.UpdatedAt.IsZero() {
			t.Fatalf("answer zero/NULL update changed immutable fields or omitted values: %+v", got)
		}
	}
	review := question.MistakeReview{ID: "r1", QuestionID: "q1", AnswerAttemptID: stringPtr("a1"),
		MistakeCategory: "category", ReviewMarkdown: "body", CorrectionMarkdown: "correction",
		KeyConclusions: "key", AIContentMarkdown: "ai", AISource: "source", CreatedAt: recordORMTime(), UpdatedAt: recordORMTime()}
	if err := reviews.Create(ctx, review); err != nil {
		t.Fatal(err)
	}
	if err := reviews.Update(ctx, question.MistakeReview{ID: "r1", QuestionID: "q2"}); err != nil {
		t.Fatal(err)
	}
	wantReview := question.MistakeReview{ID: "r1", QuestionID: "q1", CreatedAt: recordORMTime()}
	if got := readRecordORMReviewSQL(t, db, ctx, "r1"); !reflect.DeepEqual(got, wantReview) {
		t.Fatalf("review NULL/empty update: got %+v, want %+v", got, wantReview)
	}
	attachment := question.Attachment{ID: "f1", QuestionID: "q1", OwnerType: "question", OwnerID: "q1", StoredName: "zero-file"}
	if err := attachments.Create(ctx, attachment); err != nil {
		t.Fatal(err)
	}
	if got := readRecordORMAttachmentSQL(t, db, ctx, "f1"); !reflect.DeepEqual(got, attachment) {
		t.Fatalf("zero attachment create: got %+v, want %+v", got, attachment)
	}

	for name, operation := range map[string]func() error{
		"answer get": func() error { _, err := answers.GetByID(ctx, "missing"); return err },
		"answer update": func() error {
			return answers.Update(ctx, question.AnswerAttempt{ID: "missing", Result: question.AnswerResultCorrect})
		},
		"answer delete": func() error { return answers.Delete(ctx, "missing") },
		"review get":    func() error { _, err := reviews.GetByID(ctx, "missing"); return err },
		"review update": func() error {
			return reviews.Update(ctx, question.MistakeReview{ID: "missing", AnswerAttemptID: stringPtr("missing-answer")})
		},
		"review delete":     func() error { return reviews.Delete(ctx, "missing") },
		"attachment get":    func() error { _, err := attachments.GetByID(ctx, "missing"); return err },
		"attachment delete": func() error { return attachments.Delete(ctx, "missing") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, question.ErrNotFound) {
				t.Fatalf("expected original not found classification: %v", err)
			}
		})
	}
	for _, table := range []string{"answer_attempts", "mistake_reviews", "attachments"} {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE id = 'missing'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("missing operation inserted row in %s: %d, %v", table, count, err)
		}
	}
	if err := attachments.Delete(ctx, "f1"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM attachments WHERE id = 'f1'`).Scan(new(string)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("attachment delete did not remove metadata: %v", err)
	}
}

func TestRecordORMReviewOwnerAndErrorPriority(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	for _, id := range []string{"q1", "q2"} {
		seedRecordORMQuestion(t, db, ctx, id)
	}
	answers, reviews := sqlite.NewAnswerRepository(db), sqlite.NewReviewRepository(db)
	for _, id := range []string{"q1", "q2"} {
		if err := answers.Create(ctx, question.AnswerAttempt{ID: "a-" + id, QuestionID: id, Result: question.AnswerResultCorrect,
			CreatedAt: recordORMTime(), UpdatedAt: recordORMTime()}); err != nil {
			t.Fatal(err)
		}
	}
	review := question.MistakeReview{ID: "r1", QuestionID: "q1", AnswerAttemptID: stringPtr("a-q1"), ReviewMarkdown: "original",
		CreatedAt: recordORMTime(), UpdatedAt: recordORMTime()}
	if err := reviews.Create(ctx, review); err != nil {
		t.Fatal(err)
	}
	for _, answerID := range []string{"a-q2", "missing", ""} {
		invalid := review
		invalid.ID, invalid.AnswerAttemptID = "invalid", stringPtr(answerID)
		if err := reviews.Create(ctx, invalid); !errors.Is(err, question.ErrInvalidInput) {
			t.Fatalf("create association %q: %v", answerID, err)
		}
		invalid.ID, invalid.QuestionID = "r1", "q2"
		invalid.ReviewMarkdown = "must not persist"
		if err := reviews.Update(ctx, invalid); !errors.Is(err, question.ErrInvalidInput) {
			t.Fatalf("update must validate against stored question, association %q: %v", answerID, err)
		}
		if got := readRecordORMReviewSQL(t, db, ctx, "r1"); !reflect.DeepEqual(got, review) {
			t.Fatalf("invalid association changed review: %+v", got)
		}
	}
	// The supplied QuestionID is ignored for updates; ownership stays with q1.
	update := review
	update.QuestionID, update.ReviewMarkdown = "q2", "valid update"
	if err := reviews.Update(ctx, update); err != nil {
		t.Fatal(err)
	}
	if got := readRecordORMReviewSQL(t, db, ctx, "r1"); got.QuestionID != "q1" || got.ReviewMarkdown != "valid update" {
		t.Fatalf("stored owner changed: %+v", got)
	}
	if err := reviews.Update(ctx, question.MistakeReview{ID: "missing", AnswerAttemptID: stringPtr("a-q2")}); !errors.Is(err, question.ErrNotFound) {
		t.Fatalf("missing review must precede association validation: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE answer_attempts`); err != nil {
		t.Fatal(err)
	}
	err := reviews.Create(ctx, review)
	if err == nil || errors.Is(err, question.ErrInvalidInput) || errors.Is(err, question.ErrNotFound) || !strings.Contains(err.Error(), "validate review answer") {
		t.Fatalf("database validation failure classified as business error: %v", err)
	}
}

func TestRecordORMListOrderOwnerAndEmpty(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	seedRecordORMQuestion(t, db, ctx, "q1")
	seedRecordORMQuestion(t, db, ctx, "q2")
	answers, reviews, attachments := sqlite.NewAnswerRepository(db), sqlite.NewReviewRepository(db), sqlite.NewAttachmentRepository(db)
	for _, id := range []string{"b", "c", "a"} {
		at := recordORMTime()
		if id == "c" {
			at = at.Add(-time.Second)
		}
		if err := answers.Create(ctx, question.AnswerAttempt{ID: id, QuestionID: "q1", Result: question.AnswerResultCorrect, CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if err := reviews.Create(ctx, question.MistakeReview{ID: id, QuestionID: "q1", CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if err := attachments.Create(ctx, question.Attachment{ID: id, QuestionID: "q1", OwnerType: "answer", OwnerID: "same",
			StoredName: id, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []question.Attachment{
		{ID: "wrong-type", QuestionID: "q1", OwnerType: "review", OwnerID: "same", StoredName: "wrong-type", CreatedAt: recordORMTime()},
		{ID: "wrong-id", QuestionID: "q1", OwnerType: "answer", OwnerID: "different", StoredName: "wrong-id", CreatedAt: recordORMTime()},
	} {
		if err := attachments.Create(ctx, file); err != nil {
			t.Fatal(err)
		}
	}
	answerItems, err := answers.ListByQuestion(ctx, "q1")
	if err != nil {
		t.Fatal(err)
	}
	reviewItems, err := reviews.ListByQuestion(ctx, "q1")
	if err != nil {
		t.Fatal(err)
	}
	attachmentItems, err := attachments.ListByOwner(ctx, "answer", "same")
	if err != nil {
		t.Fatal(err)
	}
	if len(answerItems) != 3 || len(reviewItems) != 3 || len(attachmentItems) != 3 {
		t.Fatalf("list count/owner filter changed: %v %v %v", answerItems, reviewItems, attachmentItems)
	}
	for i, id := range []string{"b", "a", "c"} {
		if answerItems[i].ID != id || reviewItems[i].ID != id || attachmentItems[i].ID != id {
			t.Fatalf("created_at/id descending order changed at %d", i)
		}
	}
	for _, questionID := range []string{"q2", "missing"} {
		items, err := answers.ListByQuestion(ctx, questionID)
		if err != nil || items == nil || len(items) != 0 {
			t.Fatalf("empty answers must be non-nil: %v %v", items, err)
		}
		reviewItems, err := reviews.ListByQuestion(ctx, questionID)
		if err != nil || reviewItems == nil || len(reviewItems) != 0 {
			t.Fatalf("empty reviews must be non-nil: %v %v", reviewItems, err)
		}
	}
	empty, err := attachments.ListByOwner(ctx, "answer", "missing")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty attachments must be non-nil: %v %v", empty, err)
	}
}

func TestRecordORMDeleteCleanupRollback(t *testing.T) {
	for _, ownerType := range []string{"answer", "review"} {
		t.Run(ownerType, func(t *testing.T) {
			db, ctx := newSQLTestDB(t)
			seedRecordORMQuestion(t, db, ctx, "q1")
			answers, reviews, attachments := sqlite.NewAnswerRepository(db), sqlite.NewReviewRepository(db), sqlite.NewAttachmentRepository(db)
			answer := question.AnswerAttempt{ID: "owner", QuestionID: "q1", Result: question.AnswerResultCorrect,
				CreatedAt: recordORMTime(), UpdatedAt: recordORMTime()}
			if err := answers.Create(ctx, answer); err != nil {
				t.Fatal(err)
			}
			review := question.MistakeReview{ID: "owner", QuestionID: "q1", AnswerAttemptID: stringPtr("owner"),
				CreatedAt: recordORMTime(), UpdatedAt: recordORMTime()}
			if err := reviews.Create(ctx, review); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"answer", "review", "question"} {
				if err := attachments.Create(ctx, question.Attachment{ID: kind, QuestionID: "q1", OwnerType: kind, OwnerID: "owner",
					StoredName: kind, CreatedAt: recordORMTime()}); err != nil {
					t.Fatal(err)
				}
			}
			if err := attachments.Create(ctx, question.Attachment{ID: "other-owner", QuestionID: "q1", OwnerType: ownerType,
				OwnerID: "different", StoredName: "other-owner", CreatedAt: recordORMTime()}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_attachment_cleanup BEFORE DELETE ON attachments
				WHEN OLD.owner_type = '`+ownerType+`' BEGIN SELECT RAISE(ABORT, 'injected cleanup failure'); END`); err != nil {
				t.Fatal(err)
			}
			deleteOwner := answers.Delete
			if ownerType == "review" {
				deleteOwner = reviews.Delete
			}
			err := deleteOwner(ctx, "owner")
			if err == nil || errors.Is(err, question.ErrNotFound) || !strings.Contains(err.Error(), "delete "+ownerType+" attachments") {
				t.Fatalf("cleanup failure classification: %v", err)
			}
			if got := readRecordORMAnswerSQL(t, db, ctx, "owner"); !reflect.DeepEqual(got, answer) {
				t.Fatalf("answer delete not rolled back: %+v", got)
			}
			if got := readRecordORMReviewSQL(t, db, ctx, "owner"); !reflect.DeepEqual(got, review) {
				t.Fatalf("review delete/SET NULL not rolled back: %+v", got)
			}
			if _, err := attachments.GetByID(ctx, ownerType); err != nil {
				t.Fatalf("failed cleanup lost metadata: %v", err)
			}
			if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_attachment_cleanup`); err != nil {
				t.Fatal(err)
			}
			if err := deleteOwner(ctx, "owner"); err != nil {
				t.Fatal(err)
			}
			if err := deleteOwner(ctx, "owner"); !errors.Is(err, question.ErrNotFound) {
				t.Fatalf("successful delete did not remove owner: %v", err)
			}
			if _, err := attachments.GetByID(ctx, "other-owner"); err != nil {
				t.Fatalf("cleanup removed another owner with the same type: %v", err)
			}
			if _, err := attachments.GetByID(ctx, ownerType); !errors.Is(err, question.ErrNotFound) {
				t.Fatalf("successful delete did not clean attachment: %v", err)
			}
			for _, otherType := range []string{"answer", "review", "question"} {
				if otherType != ownerType {
					if _, err := attachments.GetByID(ctx, otherType); err != nil {
						t.Fatalf("cleanup removed other owner's attachment %q: %v", otherType, err)
					}
				}
			}
			if ownerType == "answer" {
				if got := readRecordORMReviewSQL(t, db, ctx, "owner"); got.AnswerAttemptID != nil {
					t.Fatalf("answer delete did not preserve ON DELETE SET NULL: %+v", got)
				}
			}
		})
	}
}

func TestRecordORMMalformedTimeErrors(t *testing.T) {
	for _, table := range []string{"answer_attempts", "mistake_reviews", "attachments"} {
		t.Run(table, func(t *testing.T) {
			db, ctx := newSQLTestDB(t)
			seedRecordORMQuestion(t, db, ctx, "q1")
			answers, reviews, attachments := sqlite.NewAnswerRepository(db), sqlite.NewReviewRepository(db), sqlite.NewAttachmentRepository(db)
			var get func() error
			var list func() error
			switch table {
			case "answer_attempts":
				if err := answers.Create(ctx, question.AnswerAttempt{ID: "bad", QuestionID: "q1", Result: question.AnswerResultCorrect}); err != nil {
					t.Fatal(err)
				}
				get = func() error { _, err := answers.GetByID(ctx, "bad"); return err }
				list = func() error { _, err := answers.ListByQuestion(ctx, "q1"); return err }
			case "mistake_reviews":
				if err := reviews.Create(ctx, question.MistakeReview{ID: "bad", QuestionID: "q1"}); err != nil {
					t.Fatal(err)
				}
				get = func() error { _, err := reviews.GetByID(ctx, "bad"); return err }
				list = func() error { _, err := reviews.ListByQuestion(ctx, "q1"); return err }
			case "attachments":
				if err := attachments.Create(ctx, question.Attachment{ID: "bad", QuestionID: "q1", OwnerType: "question", OwnerID: "q1", StoredName: "bad"}); err != nil {
					t.Fatal(err)
				}
				get = func() error { _, err := attachments.GetByID(ctx, "bad"); return err }
				list = func() error { _, err := attachments.ListByOwner(ctx, "question", "q1"); return err }
			}
			columns := []string{"created_at", "updated_at"}
			if table == "attachments" {
				columns = columns[:1]
			}
			for _, column := range columns {
				if _, err := db.ExecContext(ctx, "UPDATE "+table+" SET "+column+" = 'malformed' WHERE id = 'bad'"); err != nil {
					t.Fatal(err)
				}
				for _, operation := range []func() error{get, list} {
					err := operation()
					var parseErr *time.ParseError
					if !errors.As(err, &parseErr) || errors.Is(err, question.ErrNotFound) || !strings.Contains(err.Error(), column) {
						t.Fatalf("malformed timestamp must retain parse error chain: %v", err)
					}
				}
				if _, err := db.ExecContext(ctx, "UPDATE "+table+" SET "+column+" = ? WHERE id = 'bad'", recordORMTime().Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
