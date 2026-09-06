package question

import (
	"context"
	"errors"
	"github.com/go-playground/validator/v10"
	"reflect"
	"testing"
)

type questionCreatorFunc func(context.Context, QuestionRecord) error

func (f questionCreatorFunc) Create(ctx context.Context, q QuestionRecord) error { return f(ctx, q) }

func TestServiceCreate(t *testing.T) {
	var saved QuestionRecord
	service := NewQuestionService(questionCreatorFunc(func(_ context.Context, q QuestionRecord) error {
		saved = q
		return nil
	}))
	q, err := service.Create(context.Background(), CreateQuestionInput{Title: " Title ", Type: QuestionTypeKnowledge, BodyMarkdown: "Body", Tags: []string{" go ", "go", "sql"}})
	if err != nil {
		t.Fatal(err)
	}
	if q.ID == "" || q.Title != "Title" || q.CreatedAt.IsZero() || !q.CreatedAt.Equal(q.UpdatedAt) || !reflect.DeepEqual(q.Tags, []string{"go", "sql"}) {
		t.Fatalf("unexpected record: %+v", q)
	}
	if !reflect.DeepEqual(saved, q) {
		t.Fatalf("repository received %+v, want %+v", saved, q)
	}
}

func TestServiceCreateInvalidInput(t *testing.T) {
	badDifficulty := Difficulty("invalid")
	for _, name := range []string{"title", "body", "type", "difficulty", "tag"} {
		t.Run(name, func(t *testing.T) {
			input := CreateQuestionInput{Title: "Title", Type: QuestionTypeKnowledge, BodyMarkdown: "Body"}
			switch name {
			case "title":
				input.Title = " "
			case "body":
				input.BodyMarkdown = " "
			case "type":
				input.Type = "invalid"
			case "difficulty":
				input.Difficulty = &badDifficulty
			case "tag":
				input.Tags = []string{" "}
			}
			service := NewQuestionService(questionCreatorFunc(func(context.Context, QuestionRecord) error { t.Fatal("invalid input reached repository"); return nil }))
			q, err := service.Create(context.Background(), input)
			if !errors.Is(err, ErrInvalidInput) || q.ID != "" {
				t.Fatalf("got %+v, %v", q, err)
			}
		})
	}
}

func TestServiceCreateRepositoryError(t *testing.T) {
	want := errors.New("storage failure")
	service := NewQuestionService(questionCreatorFunc(func(context.Context, QuestionRecord) error { return want }))
	q, err := service.Create(context.Background(), CreateQuestionInput{Title: "Title", Type: QuestionTypeKnowledge, BodyMarkdown: "Body"})
	if !errors.Is(err, want) || q.ID != "" {
		t.Fatalf("got %+v, %v", q, err)
	}
}

func TestValidateCreateQuestionInput(t *testing.T) {
	empty := Difficulty("")
	hard := DifficultyHard
	for _, tc := range []struct {
		name       string
		difficulty *Difficulty
		tags       []string
		valid      bool
	}{
		{"optional", nil, nil, true},
		{"hard", &hard, []string{"go"}, true},
		{"empty difficulty", &empty, nil, false},
		{"blank tag", nil, []string{"\t "}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateQuestionInput(CreateQuestionInput{Title: "Title", Type: QuestionTypeKnowledge, BodyMarkdown: "Body", Difficulty: tc.difficulty, Tags: tc.tags})
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected invalid input, got %v", err)
			}
			var fields validator.ValidationErrors
			if !errors.As(err, &fields) || len(fields) == 0 {
				t.Fatalf("missing field errors: %v", err)
			}
		})
	}
}

func TestValidateEnumValues(t *testing.T) {
	for _, value := range []QuestionType{QuestionTypeAlgorithm, QuestionTypeKnowledge, QuestionTypeSystemDesign, QuestionTypeBehavioral, QuestionTypeOther} {
		if err := ValidateQuestionType(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []Difficulty{DifficultyEasy, DifficultyMedium, DifficultyHard} {
		if err := ValidateDifficulty(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []AnswerResult{AnswerResultSkipped, AnswerResultIncorrect, AnswerResultPartial, AnswerResultCorrect} {
		if err := ValidateAnswerResult(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, err := range []error{ValidateQuestionType("invalid"), ValidateDifficulty(""), ValidateAnswerResult("invalid")} {
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid input, got %v", err)
		}
	}
}
