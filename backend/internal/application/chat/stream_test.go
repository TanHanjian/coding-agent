package chat

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type writerStoreStub struct {
	TurnStore
	appendErr error
	events    *[]string
	messageID string
	text      string
}

func (s *writerStoreStub) AppendAssistantText(_ context.Context, messageID, text string) error {
	*s.events = append(*s.events, "store")
	s.messageID = messageID
	s.text = text
	return s.appendErr
}

type writerHubSpy struct {
	GenerationHub
	events    *[]string
	messageID string
	text      string
}

func (h *writerHubSpy) PublishText(messageID, text string) {
	*h.events = append(*h.events, "hub")
	h.messageID = messageID
	h.text = text
}

func TestGenerationTextWriterCommitsBeforePublishing(t *testing.T) {
	events := make([]string, 0, 2)
	store := &writerStoreStub{events: &events}
	hub := &writerHubSpy{events: &events}
	writer := newGenerationTextWriter(store, hub, "m-assistant")

	if err := writer.CommitText(context.Background(), "第一批文本"); err != nil {
		t.Fatal(err)
	}

	if store.messageID != "m-assistant" || store.text != "第一批文本" {
		t.Fatalf("unexpected store input: id=%q text=%q", store.messageID, store.text)
	}
	if hub.messageID != "m-assistant" || hub.text != "第一批文本" {
		t.Fatalf("unexpected hub input: id=%q text=%q", hub.messageID, hub.text)
	}
	if want := []string{"store", "hub"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("unexpected side-effect order: got=%v want=%v", events, want)
	}
}

func TestGenerationTextWriterDoesNotPublishWhenCommitFails(t *testing.T) {
	events := make([]string, 0, 2)
	commitErr := errors.New("sqlite unavailable")
	store := &writerStoreStub{appendErr: commitErr, events: &events}
	hub := &writerHubSpy{events: &events}
	writer := newGenerationTextWriter(store, hub, "m-assistant")

	err := writer.CommitText(context.Background(), "不会广播")
	if !errors.Is(err, commitErr) {
		t.Fatalf("got error %v, want %v", err, commitErr)
	}
	if want := []string{"store"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("unexpected side effects: got=%v want=%v", events, want)
	}
}

func TestGenerationTextWriterIgnoresEmptyBatch(t *testing.T) {
	events := make([]string, 0)
	store := &writerStoreStub{events: &events}
	hub := &writerHubSpy{events: &events}
	writer := newGenerationTextWriter(store, hub, "m-assistant")

	if err := writer.CommitText(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("empty batch must not have side effects: %v", events)
	}
}
