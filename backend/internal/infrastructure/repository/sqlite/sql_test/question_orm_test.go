package sqlite_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
)

func TestQuestionORMLegacyFixtureRoundTripAndZeroValues(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	repo := sqlite.NewQuestionRepository(db)
	created := "2025-01-02T03:04:05.123456789Z"
	updated := "2025-02-03T04:05:06.987654321Z"
	if _, err := db.ExecContext(ctx, `INSERT INTO questions
		(id, title, type, body_markdown, difficulty, source_name, source_url, is_archived, created_at, updated_at)
		VALUES ('legacy', 'Old title', 'knowledge', 'Old body', 'hard', 'Old source', 'https://old', 1, ?, ?)`, created, updated); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO question_tags (question_id, tag) VALUES ('legacy', 'z'), ('legacy', 'a')`); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != question.QuestionTypeKnowledge || got.Difficulty == nil || *got.Difficulty != question.DifficultyHard ||
		!got.IsArchived || got.CreatedAt.Format(time.RFC3339Nano) != created || got.UpdatedAt.Format(time.RFC3339Nano) != updated ||
		!reflect.DeepEqual(got.Tags, []string{"a", "z"}) {
		t.Fatalf("legacy mapping: %+v", got)
	}
	search, err := repo.Search(ctx, question.QuestionSearchQuery{Archived: boolPtr(true)})
	if err != nil || len(search.Items) != 1 || !reflect.DeepEqual(search.Items[0], got) {
		t.Fatalf("legacy search mapping: %+v, %v", search, err)
	}
	// Empty strings, false (SQLite integer 0), NULL and a zero timestamp must
	// reach the database. Updating a record must not rewrite its created_at.
	got.Title, got.BodyMarkdown, got.SourceName, got.SourceURL = "", "", "", ""
	got.Difficulty, got.IsArchived, got.Tags = nil, false, nil
	got.CreatedAt, got.UpdatedAt = time.Time{}, time.Time{}
	if err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	var title, body, source, url, rawCreated, rawUpdated, timeType, difficultyType string
	var archived int
	var difficulty *string
	if err := db.QueryRowContext(ctx, `SELECT title, body_markdown, source_name, source_url, is_archived, difficulty,
		created_at, updated_at, typeof(updated_at), typeof(difficulty) FROM questions WHERE id = 'legacy'`).
		Scan(&title, &body, &source, &url, &archived, &difficulty, &rawCreated, &rawUpdated, &timeType, &difficultyType); err != nil {
		t.Fatal(err)
	}
	if title != "" || body != "" || source != "" || url != "" || archived != 0 || difficulty != nil || difficultyType != "null" ||
		rawCreated != created || rawUpdated != "0001-01-01T00:00:00Z" || timeType != "text" {
		t.Fatalf("zero update: %q %q %q %q %d %v %q %q %q %q", title, body, source, url, archived, difficulty, rawCreated, rawUpdated, timeType, difficultyType)
	}
	got, err = repo.GetByID(ctx, "legacy")
	if err != nil || got.Difficulty != nil || !got.UpdatedAt.IsZero() || !reflect.DeepEqual(got.Tags, []string{"a", "z"}) {
		t.Fatalf("zero round trip: %+v, %v", got, err)
	}
	// New ORM writes remain readable by independent SQL, including caller clocks
	// in a non-UTC location and nullable difficulty.
	got.ID, got.Tags = "new", []string{}
	got.CreatedAt = time.Date(2025, 3, 4, 5, 6, 7, 123456789, time.FixedZone("offset", 8*3600))
	got.UpdatedAt = got.CreatedAt.Add(time.Nanosecond)
	if err := repo.Create(ctx, got); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT created_at, updated_at, typeof(created_at), typeof(difficulty) FROM questions WHERE id = 'new'`).
		Scan(&rawCreated, &rawUpdated, &timeType, &difficultyType); err != nil {
		t.Fatal(err)
	}
	if rawCreated != got.CreatedAt.UTC().Format(time.RFC3339Nano) || rawUpdated != got.UpdatedAt.UTC().Format(time.RFC3339Nano) || timeType != "text" || difficultyType != "null" {
		t.Fatalf("new SQL mapping: %q %q %q %q", rawCreated, rawUpdated, timeType, difficultyType)
	}
	got, err = repo.GetByID(ctx, "new")
	if err != nil || got.Tags == nil || len(got.Tags) != 0 {
		t.Fatalf("empty tags: %+v, %v", got, err)
	}
	for _, value := range []bool{true, false} {
		if err := repo.SetArchived(ctx, "new", value); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetByID(ctx, "new")
		if err != nil || got.IsArchived != value {
			t.Fatalf("archive %v: %+v, %v", value, got, err)
		}
	}
}

func TestQuestionORMMissingAndCorruptRecords(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	repo := sqlite.NewQuestionRepository(db)
	for name, operation := range map[string]func() error{
		"exists": func() error { return repo.Exists(ctx, "missing") },
		"get":    func() error { _, err := repo.GetByID(ctx, "missing"); return err },
		"detail": func() error { _, err := repo.GetDetail(ctx, "missing"); return err },
		"update": func() error {
			return repo.Update(ctx, question.QuestionRecord{ID: "missing", Type: question.QuestionTypeOther, Tags: []string{"new"}})
		},
		"archive": func() error { return repo.SetArchived(ctx, "missing", false) },
		"delete":  func() error { return repo.DeletePermanent(ctx, "missing") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, question.ErrNotFound) {
				t.Fatalf("expected not found: %v", err)
			}
		})
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM questions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("missing operations inserted records: %d, %v", count, err)
	}
	createSQLQuestion(t, ctx, db, "corrupt", false)
	for _, column := range []string{"created_at", "updated_at"} {
		if _, err := db.ExecContext(ctx, "UPDATE questions SET "+column+" = 'invalid' WHERE id = 'corrupt'"); err != nil {
			t.Fatal(err)
		}
		if record, err := repo.GetByID(ctx, "corrupt"); err == nil || !reflect.DeepEqual(record, question.QuestionRecord{}) {
			t.Fatalf("corrupt get succeeded: %+v, %v", record, err)
		}
		if result, err := repo.Search(ctx, question.QuestionSearchQuery{}); err == nil || !reflect.DeepEqual(result, question.QuestionSearchResult{}) {
			t.Fatalf("corrupt search succeeded: %+v, %v", result, err)
		}
		if err := repo.Exists(ctx, "corrupt"); err != nil {
			t.Fatalf("existence should not parse timestamps: %v", err)
		}
		if _, err := db.ExecContext(ctx, "UPDATE questions SET "+column+" = '2025-01-01T00:00:00Z' WHERE id = 'corrupt'"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQuestionORMSearchAllTextFields(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	repo := sqlite.NewQuestionRepository(db)
	// Populate through SQL so this tests the legacy search contract independently
	// of the answer/review repositories being migrated in parallel.
	fields := map[string][]string{
		"questions":       {"title", "body_markdown", "source_name", "source_url"},
		"answer_attempts": {"body_markdown", "code"},
		"mistake_reviews": {"mistake_category", "review_markdown", "correction_markdown", "key_conclusions", "ai_content_markdown"},
	}
	for table, columns := range fields {
		for _, column := range columns {
			t.Run(table+"/"+column, func(t *testing.T) {
				id := table + "_" + column
				createSQLQuestion(t, ctx, db, id, false)
				switch table {
				case "answer_attempts":
					if _, err := db.ExecContext(ctx, `INSERT INTO answer_attempts (id, question_id, result, created_at, updated_at) VALUES (?, ?, 'correct', '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`, id, id); err != nil {
						t.Fatal(err)
					}
				case "mistake_reviews":
					if _, err := db.ExecContext(ctx, `INSERT INTO mistake_reviews (id, question_id, created_at, updated_at) VALUES (?, ?, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`, id, id); err != nil {
						t.Fatal(err)
					}
				}
				// Table and column names are fixed fixture values above.
				if _, err := db.ExecContext(ctx, "UPDATE "+table+" SET "+column+" = ? WHERE id = ?", "MiXeD "+id, id); err != nil {
					t.Fatal(err)
				}
				result, err := repo.Search(ctx, question.QuestionSearchQuery{Text: "  MIXED " + id + "  "})
				if err != nil || result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != id {
					t.Fatalf("text field search: %+v, %v", result, err)
				}
			})
		}
	}
	// LIKE wildcard semantics intentionally remain unchanged.
	result, err := repo.Search(ctx, question.QuestionSearchQuery{Text: "%"})
	if err != nil || result.Total != 11 {
		t.Fatalf("LIKE wildcard search: %+v, %v", result, err)
	}
}

func TestQuestionORMSearchCombinationsAndPagination(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	repo := sqlite.NewQuestionRepository(db)
	now := time.Date(2025, 1, 1, 0, 0, 0, 123456789, time.UTC)
	hard, easy := question.DifficultyHard, question.DifficultyEasy
	for _, record := range []question.QuestionRecord{
		{ID: "a", Difficulty: &hard, Tags: []string{"sql", "go"}},
		{ID: "b", Difficulty: &hard, Tags: []string{"go", "sql"}},
		{ID: "c", Difficulty: &easy, Tags: []string{"go"}},
		{ID: "d", IsArchived: true, Tags: []string{"sql", "go"}},
		{ID: "e", Tags: []string{}},
	} {
		record.Title, record.Type, record.BodyMarkdown = "same", question.QuestionTypeAlgorithm, "body"
		record.SourceName, record.SourceURL = "source", "https://source"
		record.CreatedAt, record.UpdatedAt = now, now
		if err := repo.Create(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b", "d"} {
		for n := 0; n < 3; n++ {
			child := fmt.Sprintf("%s-%d", id, n)
			if _, err := db.ExecContext(ctx, `INSERT INTO answer_attempts (id, question_id, body_markdown, result, created_at, updated_at)
				VALUES (?, ?, 'needle', 'correct', ?, ?)`, child, id, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO mistake_reviews (id, question_id, review_markdown, created_at, updated_at)
				VALUES (?, ?, 'needle', ?, ?)`, child, id, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		name  string
		query question.QuestionSearchQuery
		ids   []string
	}{
		{"default", question.QuestionSearchQuery{}, []string{"e", "c", "b", "a"}},
		{"empty filters", question.QuestionSearchQuery{Types: []question.QuestionType{}, Difficulties: []question.Difficulty{}, Results: []question.AnswerResult{}, Tags: []string{" ", ""}, Text: " ", Source: " "}, []string{"e", "c", "b", "a"}},
		{"combination", question.QuestionSearchQuery{Text: "NEEDLE", Types: []question.QuestionType{question.QuestionTypeKnowledge, question.QuestionTypeAlgorithm}, Difficulties: []question.Difficulty{question.DifficultyHard}, Results: []question.AnswerResult{question.AnswerResultCorrect, question.AnswerResultPartial}, Tags: []string{" go ", "sql"}, Source: " source ", HasMistakes: boolPtr(true)}, []string{"b", "a"}},
		{"source URL", question.QuestionSearchQuery{Source: "https://source"}, []string{"e", "c", "b", "a"}},
		{"source exact", question.QuestionSearchQuery{Source: "sour"}, []string{}},
		{"no mistakes", question.QuestionSearchQuery{HasMistakes: boolPtr(false)}, []string{"e", "c"}},
		{"has mistakes", question.QuestionSearchQuery{HasMistakes: boolPtr(true)}, []string{"b", "a"}},
		{"tags intersection", question.QuestionSearchQuery{Tags: []string{"go", "sql"}}, []string{"b", "a"}},
		{"missing tag", question.QuestionSearchQuery{Tags: []string{"go", "missing"}}, []string{}},
		{"wrong result", question.QuestionSearchQuery{Results: []question.AnswerResult{question.AnswerResultIncorrect}}, []string{}},
		{"wrong type", question.QuestionSearchQuery{Types: []question.QuestionType{question.QuestionTypeKnowledge}}, []string{}},
		{"difficulty excludes NULL", question.QuestionSearchQuery{Difficulties: []question.Difficulty{question.DifficultyEasy, question.DifficultyHard}}, []string{"c", "b", "a"}},
		{"archived", question.QuestionSearchQuery{Archived: boolPtr(true), Text: "needle", HasMistakes: boolPtr(true)}, []string{"d"}},
		{"active", question.QuestionSearchQuery{Archived: boolPtr(false)}, []string{"e", "c", "b", "a"}},
		{"unknown sort falls back", question.QuestionSearchQuery{SortBy: "title; DROP TABLE questions", SortDirection: "bad"}, []string{"e", "c", "b", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := repo.Search(ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				ids = append(ids, item.ID)
				if item.Tags == nil {
					t.Fatalf("nil tags for %s", item.ID)
				}
				if item.ID == "a" || item.ID == "b" || item.ID == "d" {
					if !reflect.DeepEqual(item.Tags, []string{"go", "sql"}) {
						t.Fatalf("tags for %s: %v", item.ID, item.Tags)
					}
				}
			}
			if result.Items == nil || !reflect.DeepEqual(ids, tc.ids) || result.Total != len(tc.ids) || result.HasNext || result.Page != 1 || result.PageSize != 20 {
				t.Fatalf("search: %+v, want IDs %v", result, tc.ids)
			}
		})
	}
	for _, sortBy := range []string{"created_at", "updated_at", "title", "difficulty", "type"} {
		for _, direction := range []string{"asc", "desc"} {
			for page := 1; page <= 3; page++ {
				t.Run(fmt.Sprintf("%s/%s/page%d", sortBy, direction, page), func(t *testing.T) {
					result, err := repo.Search(ctx, question.QuestionSearchQuery{Text: "needle", Tags: []string{"go", "sql"}, SortBy: sortBy, SortDirection: direction, Page: page, PageSize: 1})
					if err != nil {
						t.Fatal(err)
					}
					want := []string{"a", "b"}
					if direction == "desc" {
						want = []string{"b", "a"}
					}
					if result.Total != 2 || result.HasNext != (page == 1) || result.Page != page || result.PageSize != 1 || result.Items == nil {
						t.Fatalf("pagination: %+v", result)
					}
					if page <= 2 {
						if len(result.Items) != 1 || result.Items[0].ID != want[page-1] {
							t.Fatalf("tie order: %+v, want %s", result, want[page-1])
						}
					} else if len(result.Items) != 0 {
						t.Fatalf("expected empty page: %+v", result)
					}
				})
			}
		}
	}
	for _, query := range []question.QuestionSearchQuery{{Page: -1}, {PageSize: 101}, {PageSize: -1}} {
		if result, err := repo.Search(ctx, query); !errors.Is(err, question.ErrInvalidInput) || !reflect.DeepEqual(result, question.QuestionSearchResult{}) {
			t.Fatalf("invalid search: %+v, %v", result, err)
		}
	}
}

func TestQuestionORMTagFailureRollsBackWholeUpdate(t *testing.T) {
	for _, failure := range []string{"delete", "later insert batch"} {
		t.Run(failure, func(t *testing.T) {
			db, ctx := newSQLTestDB(t)
			createSQLQuestion(t, ctx, db, "q", false)
			repo := sqlite.NewQuestionRepository(db)
			original, err := repo.GetByID(ctx, "q")
			if err != nil {
				t.Fatal(err)
			}
			updated := original
			updated.Title, updated.UpdatedAt = "changed", original.UpdatedAt.Add(time.Hour)
			if failure == "delete" {
				if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_tag_delete BEFORE DELETE ON question_tags
					BEGIN SELECT RAISE(ABORT, 'tag delete rejected'); END`); err != nil {
					t.Fatal(err)
				}
				updated.Tags = []string{}
			} else {
				updated.Tags = make([]string, 101)
				for i := 0; i < 100; i++ {
					updated.Tags[i] = fmt.Sprintf("tag-%03d", i)
				}
				updated.Tags[100] = updated.Tags[0]
			}
			if err := repo.Update(ctx, updated); err == nil {
				t.Fatal("expected tag failure")
			}
			got, err := repo.GetByID(ctx, "q")
			if err != nil || !reflect.DeepEqual(got, original) {
				t.Fatalf("update left changes: %+v, %v; want %+v", got, err, original)
			}
			if failure == "later insert batch" {
				updated.ID = "new"
				if err := repo.Create(ctx, updated); err == nil {
					t.Fatal("expected create tag failure")
				}
				if err := repo.Exists(ctx, "new"); !errors.Is(err, question.ErrNotFound) {
					t.Fatalf("create left question: %v", err)
				}
				var count int
				if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM question_tags WHERE question_id = 'new'`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("create left tags: %d, %v", count, err)
				}
			}
		})
	}
}

func TestQuestionORMDeleteCascadeAndDetail(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLQuestion(t, ctx, db, "parent", false)
	for _, statement := range []string{
		`INSERT INTO answer_attempts (id, question_id, result, created_at, updated_at) VALUES ('answer', 'parent', 'correct', '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`,
		`INSERT INTO mistake_reviews (id, question_id, answer_attempt_id, created_at, updated_at) VALUES ('review', 'parent', 'answer', '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`,
		`INSERT INTO attachments (id, question_id, owner_type, owner_id, original_name, stored_name, mime_type, size_bytes, sha256, created_at) VALUES ('attachment', 'parent', 'question', 'parent', 'file', 'stored', 'text/plain', 0, 'sha', '2025-01-01T00:00:00Z')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	repo := sqlite.NewQuestionRepository(db)
	detail, err := repo.GetDetail(ctx, "parent")
	if err != nil || detail.Question.ID != "parent" || len(detail.Answers) != 1 || len(detail.Reviews) != 1 || len(detail.Attachments) != 1 {
		t.Fatalf("detail: %+v, %v", detail, err)
	}
	if err := repo.DeletePermanent(ctx, "parent"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"questions", "question_tags", "answer_attempts", "mistake_reviews", "attachments"} {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("cascade %s: %d, %v", table, count, err)
		}
	}
}
