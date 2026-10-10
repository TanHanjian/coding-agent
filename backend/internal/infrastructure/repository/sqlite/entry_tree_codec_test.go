package sqlite

import (
	"testing"

	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestEncodeDecodeCompaction(t *testing.T) {
	t.Run("encodes and decodes compaction entry", func(t *testing.T) {
		compaction := conversation.EntryCompaction{
			Schema:           "structured-v1",
			FirstKeptEntryID: "e_kept123",
			SourceBoundary:   []conversation.EntryID{"e_1", "e_2", "e_3"},
			TokensBefore:     1000,
			Summary: conversation.CompactionSummary{
				Goal:           "User wants to learn about Go",
				ConfirmedFacts: []string{"User is a beginner", "Prefers examples"},
				Decisions:      []string{"Start with basics"},
				Context:        "Learning Go programming language",
			},
		}

		encoded, err := EncodeEntryCompaction(compaction)
		if err != nil {
			t.Fatalf("EncodeEntryCompaction() error = %v", err)
		}

		decoded, err := DecodeEntryCompaction(conversation.CompactionPayloadV1, encoded)
		if err != nil {
			t.Fatalf("DecodeEntryCompaction() error = %v", err)
		}

		if decoded.Schema != compaction.Schema {
			t.Errorf("Schema = %s, want %s", decoded.Schema, compaction.Schema)
		}
		if decoded.FirstKeptEntryID != compaction.FirstKeptEntryID {
			t.Errorf("FirstKeptEntryID = %s, want %s", decoded.FirstKeptEntryID, compaction.FirstKeptEntryID)
		}
		if len(decoded.SourceBoundary) != len(compaction.SourceBoundary) {
			t.Errorf("len(SourceBoundary) = %d, want %d", len(decoded.SourceBoundary), len(compaction.SourceBoundary))
		}
		if decoded.TokensBefore != compaction.TokensBefore {
			t.Errorf("TokensBefore = %d, want %d", decoded.TokensBefore, compaction.TokensBefore)
		}
		if decoded.Summary.Goal != compaction.Summary.Goal {
			t.Errorf("Summary.Goal = %s, want %s", decoded.Summary.Goal, compaction.Summary.Goal)
		}
	})

	t.Run("rejects unsupported payload version", func(t *testing.T) {
		data := []byte(`{"schema":"structured-v1"}`)
		_, err := DecodeEntryCompaction(999, data)
		if err == nil {
			t.Error("DecodeEntryCompaction() expected error for unsupported version, got nil")
		}
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		data := []byte(`{invalid json}`)
		_, err := DecodeEntryCompaction(conversation.CompactionPayloadV1, data)
		if err == nil {
			t.Error("DecodeEntryCompaction() expected error for invalid JSON, got nil")
		}
	})
}
