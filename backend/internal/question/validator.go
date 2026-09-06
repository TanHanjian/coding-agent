package question

import (
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
)

var inputValidator = newInputValidator()

func newInputValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterAlias("question_type", "oneof=algorithm knowledge system_design behavioral other")
	v.RegisterAlias("question_difficulty", "oneof=easy medium hard")
	v.RegisterAlias("answer_result", "oneof=skipped incorrect partial correct")
	if err := v.RegisterValidation("notblank", func(fl validator.FieldLevel) bool {
		return strings.TrimSpace(fl.Field().String()) != ""
	}); err != nil {
		panic(err)
	}
	return v
}

func validationError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalidInput, err)
}

// ValidateCreateQuestionInput 检查创建题目的字段和标签是否合法。
func ValidateCreateQuestionInput(input CreateQuestionInput) error {
	return validationError(inputValidator.Struct(input))
}

// ValidateQuestionType 检查题型是否属于系统支持的五种题型。
func ValidateQuestionType(value QuestionType) error {
	return validationError(inputValidator.Var(value, "question_type"))
}

// ValidateDifficulty 检查难度值是否合法。
func ValidateDifficulty(value Difficulty) error {
	return validationError(inputValidator.Var(value, "question_difficulty"))
}

// ValidateAnswerResult 检查作答结果值是否合法。
func ValidateAnswerResult(value AnswerResult) error {
	return validationError(inputValidator.Var(value, "answer_result"))
}

// NormalizeSearchQuery 填充分页默认值，并检查分页参数范围。
func NormalizeSearchQuery(query QuestionSearchQuery) (QuestionSearchQuery, error) {
	if query.Page == 0 {
		query.Page = 1
	}
	if query.PageSize == 0 {
		query.PageSize = 20
	}
	if query.Page < 1 || query.PageSize < 1 || query.PageSize > 100 {
		return QuestionSearchQuery{}, ErrInvalidInput
	}
	return query, nil
}
