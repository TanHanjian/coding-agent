package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"interview-memory-agent/backend/internal/application/hot100"
	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/infrastructure/config"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "report missing Hot 100 records without writing them")
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	paths := storage.NewPaths(cfg.DataDir)
	if err := paths.Ensure(); err != nil {
		fatal(err)
	}
	db, err := storage.Open(context.Background(), paths.Database)
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(context.Background(), db.DB); err != nil {
		fatal(err)
	}
	repo := sqlite.NewQuestionRepository(db)
	service := question.NewService(question.Dependencies{Creator: repo, Searcher: repo})
	importer, err := hot100.NewImporter(service)
	if err != nil {
		fatal(err)
	}
	result, err := importer.Import(context.Background(), *dryRun)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("Hot 100: total=%d created=%d skipped=%d dry_run=%t\n", result.Total, result.Created, result.Skipped, *dryRun)
}

func fatal(err error) { slog.Error("import Hot 100", "error", err); os.Exit(1) }
