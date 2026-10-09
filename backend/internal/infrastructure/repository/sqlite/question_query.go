package sqlite

import (
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"interview-memory-agent/backend/internal/domain/question"
)

func questionFilterScopes(query question.QuestionSearchQuery) []func(*gorm.DB) *gorm.DB {
	return []func(*gorm.DB) *gorm.DB{
		questionTextScope(query.Text), questionTypesScope(query.Types),
		questionDifficultiesScope(query.Difficulties), questionResultsScope(query.Results),
		questionTagsScope(query.Tags), questionSourceScope(query.Source),
		questionArchivedScope(query.Archived), questionMistakesScope(query.HasMistakes),
	}
}

func questionTextScope(text string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		text = strings.TrimSpace(strings.ToLower(text))
		if text == "" {
			return db
		}
		pattern := "%" + text + "%"
		answers := db.Session(&gorm.Session{NewDB: true}).Table("answer_attempts").Select("1").
			Where("answer_attempts.question_id = questions.id").
			Where("lower(body_markdown) LIKE ? OR lower(code) LIKE ?", pattern, pattern)
		reviews := db.Session(&gorm.Session{NewDB: true}).Table("mistake_reviews").Select("1").
			Where("mistake_reviews.question_id = questions.id").
			Where("lower(mistake_category) LIKE ? OR lower(review_markdown) LIKE ? OR lower(correction_markdown) LIKE ? OR lower(key_conclusions) LIKE ? OR lower(ai_content_markdown) LIKE ?", pattern, pattern, pattern, pattern, pattern)
		return db.Where("lower(questions.title) LIKE ? OR lower(questions.body_markdown) LIKE ? OR lower(questions.source_name) LIKE ? OR lower(questions.source_url) LIKE ? OR EXISTS (?) OR EXISTS (?)", pattern, pattern, pattern, pattern, answers, reviews)
	}
}

func questionTypesScope(types []question.QuestionType) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if len(types) == 0 {
			return db
		}
		return db.Where("questions.type IN ?", types)
	}
}

func questionDifficultiesScope(difficulties []question.Difficulty) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if len(difficulties) == 0 {
			return db
		}
		return db.Where("questions.difficulty IN ?", difficulties)
	}
}

func questionResultsScope(results []question.AnswerResult) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if len(results) == 0 {
			return db
		}
		answers := db.Session(&gorm.Session{NewDB: true}).Table("answer_attempts").Select("1").
			Where("answer_attempts.question_id = questions.id").Where("result IN ?", results)
		return db.Where("EXISTS (?)", answers)
	}
}

func questionTagsScope(tags []string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		for _, tag := range tags {
			if tag = strings.TrimSpace(tag); tag == "" {
				continue
			}
			tagQuery := db.Session(&gorm.Session{NewDB: true}).Model(&questionTagRow{}).Select("1").
				Where("question_tags.question_id = questions.id").Where("tag = ?", tag)
			db = db.Where("EXISTS (?)", tagQuery)
		}
		return db
	}
}

func questionSourceScope(source string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if source = strings.TrimSpace(source); source == "" {
			return db
		}
		return db.Where("questions.source_name = ? OR questions.source_url = ?", source, source)
	}
}

func questionArchivedScope(archived *bool) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		value := false
		if archived != nil {
			value = *archived
		}
		return db.Where("questions.is_archived = ?", value)
	}
}

func questionMistakesScope(hasMistakes *bool) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if hasMistakes == nil {
			return db
		}
		reviews := db.Session(&gorm.Session{NewDB: true}).Table("mistake_reviews").Select("1").
			Where("mistake_reviews.question_id = questions.id")
		if *hasMistakes {
			return db.Where("EXISTS (?)", reviews)
		}
		return db.Where("NOT EXISTS (?)", reviews)
	}
}

func questionOrderScope(sortBy, direction string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		columns := map[string]string{
			"created_at": "created_at", "updated_at": "updated_at", "title": "title",
			"difficulty": "difficulty", "type": "type",
		}
		column := columns[sortBy]
		if column == "" {
			column = "updated_at"
		}
		desc := !strings.EqualFold(direction, "asc")
		return db.Order(clause.OrderByColumn{Column: clause.Column{Table: "questions", Name: column}, Desc: desc}).
			Order(clause.OrderByColumn{Column: clause.Column{Table: "questions", Name: "id"}, Desc: desc})
	}
}
