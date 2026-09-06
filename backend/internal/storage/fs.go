package storage

import (
	"fmt"
	"os"
)

func mkdirAll(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create data directory %q: %w", path, err)
	}
	return nil
}
