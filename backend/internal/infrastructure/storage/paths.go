package storage

import "path/filepath"

type Paths struct {
	Root        string
	Data        string
	Attachments string
	Backups     string
	Logs        string
	Database    string
}

func NewPaths(root string) Paths {
	return Paths{Root: root, Data: filepath.Join(root, "data"), Attachments: filepath.Join(root, "attachments"), Backups: filepath.Join(root, "backups"), Logs: filepath.Join(root, "logs"), Database: filepath.Join(root, "data", "app.db")}
}

func (p Paths) Ensure() error {
	for _, path := range []string{p.Root, p.Data, p.Attachments, p.Backups, p.Logs} {
		if err := ensureDirectory(path); err != nil {
			return err
		}
	}
	return nil
}

func ensureDirectory(path string) error { return mkdirAll(path) }
