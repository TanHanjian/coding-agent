package sqlite

import (
	"fmt"

	"gorm.io/gorm"

	"interview-memory-agent/backend/internal/domain/domainerr"
)

func requireORMAffected(result *gorm.DB, operation string) error {
	if result.Error != nil {
		return fmt.Errorf("%s: %w", operation, result.Error)
	}
	if result.RowsAffected == 0 {
		return domainerr.ErrNotFound
	}
	return nil
}
