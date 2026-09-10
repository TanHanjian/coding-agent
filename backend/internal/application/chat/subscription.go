package chat

import "interview-memory-agent/backend/internal/domain/conversation"

// GenerationSnapshot 是订阅建立瞬间的完整可见状态。
type GenerationSnapshot struct {
	AssistantMessageID string
	Content            string
	Status             conversation.MessageStatus
}

type GenerationUpdate struct {
	Kind   GenerationUpdateKind
	Text   string
	Status conversation.MessageStatus
}

type GenerationUpdateKind string

const (
	GenerationUpdateDelta    GenerationUpdateKind = "delta"
	GenerationUpdateTerminal GenerationUpdateKind = "terminal"
)

// GenerationHub 是单进程内仍在运行的生成任务及其订阅者集合。
type GenerationHub interface {
	Open(conversation.Message)
	PublishText(assistantMessageID, text string)
	Complete(conversation.Message)
	Subscribe(assistantMessageID string) (GenerationSubscription, bool)
}

type GenerationSubscription struct {
	Snapshot GenerationSnapshot
	Updates  <-chan GenerationUpdate
	close    func()
}

func (s GenerationSubscription) Close() {
	if s.close != nil {
		s.close()
	}
}
