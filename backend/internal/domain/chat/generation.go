package chat

import (
	"context"
	"strings"
	"sync"
	"time"

	"interview-memory-agent/backend/internal/domain/domainerr"
)

// generationRun 把一次已持久化的聊天消息对与它独立的生成上下文关联起来。
// 它在 Start 成功创建新消息对后构造；HTTP 请求结束不会取消 Context。
type generationRun struct {
	Context context.Context
	Cancel  context.CancelFunc
	Request Request
	Sink    *bufferedTextSink
}

// generationTextWriter 是应用基础设施的唯一连接点。它不认识 HTTP：
// CommitText 的后续实现负责批量写 Store，再调用 Hub.PublishText。
type generationTextWriter struct {
	store              TurnStore
	hub                GenerationHub
	assistantMessageID string
}

func newGenerationTextWriter(store TurnStore, hub GenerationHub, assistantMessageID string) *generationTextWriter {
	return &generationTextWriter{store: store, hub: hub, assistantMessageID: assistantMessageID}
}

// CommitText 处理已经聚合完成的一批文本：先写 SQLite，再广播 Hub。
// 它不维护 buffer，buffer 的生命周期和阈值策略由 bufferedTextSink 负责。
func (w *generationTextWriter) CommitText(ctx context.Context, textBatch string) error {
	if textBatch == "" {
		return nil
	}
	return domainerr.ErrNotImplemented
}

// TextBufferPolicy 是一条 assistant Message 的内存缓存策略。字节数使用 UTF-8
// 字节计数；它仅影响批量持久化频率，不改变发送给模型或前端的文本内容。
type TextBufferPolicy struct {
	MaxBytes int
	MaxWait  time.Duration
}

var DefaultTextBufferPolicy = TextBufferPolicy{
	MaxBytes: 512,
	MaxWait:  300 * time.Millisecond,
}

// bufferedTextSink 的维度是一条 assistant Message，也就是一个 Generation。
// mu 保护缓存和定时器；flushMu 串行化定时 Flush、阈值 Flush 与终态 Flush 的
// 提交动作，保证文本不会重复提交或乱序。
type bufferedTextSink struct {
	// writer 负责真实副作用：SQLite 追加成功后再向 Hub 广播 delta。
	writer *generationTextWriter

	// assistantMessageID 是这段内存缓存唯一对应的消息，不按 Conversation 共享。
	assistantMessageID string
	policy             TextBufferPolicy

	// pending 保存尚未落库、尚未广播的一小段连续文本。
	pending      strings.Builder
	pendingBytes int
	firstErr     error

	// timer 在 pending 首次非空时启动，确保 MaxWait 是真正的最长等待时间，
	// 而不是等到下一个 chunk 到来后才被动检查。
	timer   *time.Timer
	timerID uint64

	mu      sync.Mutex
	flushMu sync.Mutex
}

func newBufferedTextSink(writer *generationTextWriter, assistantMessageID string, policy TextBufferPolicy) *bufferedTextSink {
	if policy.MaxBytes <= 0 {
		policy.MaxBytes = DefaultTextBufferPolicy.MaxBytes
	}
	if policy.MaxWait <= 0 {
		policy.MaxWait = DefaultTextBufferPolicy.MaxWait
	}
	return &bufferedTextSink{
		writer:             writer,
		assistantMessageID: assistantMessageID,
		policy:             policy,
	}
}

func (s *bufferedTextSink) Flush(ctx context.Context) error {
	return s.flush(ctx, 0)
}

// flush 在 expectedTimerID 非零时只允许对应的定时器提交。较旧的定时器即使
// 已经触发但尚未抢到锁，也不能错误地提交下一批文本。
func (s *bufferedTextSink) flush(ctx context.Context, expectedTimerID uint64) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedTimerID != 0 && expectedTimerID != s.timerID {
		return nil
	}
	if s.firstErr != nil {
		return s.firstErr
	}
	if s.pendingBytes == 0 {
		s.stopTimerLocked()
		return nil
	}

	// 提交成功前不能清空 pending；否则数据库临时失败会丢掉最后一批文本。
	text := s.pending.String()
	s.stopTimerLocked()

	if err := s.writer.CommitText(ctx, text); err != nil {
		s.firstErr = err
		return err
	}

	s.pending.Reset()
	s.pendingBytes = 0
	return nil
}

// WriteChunk 接收模型产生的短文本，并在 MaxBytes 达到时立即触发 Flush。
// MaxWait 由首次写入时启动的 timer 保证。
func (s *bufferedTextSink) WriteChunk(ctx context.Context, chunk string) error {
	if chunk == "" {
		return nil
	}

	s.mu.Lock()
	if s.firstErr != nil {
		err := s.firstErr
		s.mu.Unlock()
		return err
	}
	s.pending.WriteString(chunk)
	s.pendingBytes += len(chunk)
	s.startTimerLocked(ctx)
	shouldFlush := s.pendingBytes >= s.policy.MaxBytes
	s.mu.Unlock()

	if !shouldFlush {
		return nil
	}
	return s.Flush(ctx)
}

func (s *bufferedTextSink) startTimerLocked(ctx context.Context) {
	if s.timer != nil {
		return
	}
	s.timerID++
	timerID := s.timerID
	s.timer = time.AfterFunc(s.policy.MaxWait, func() {
		// timer 没有同步调用方；flush 会保存错误，后续 WriteChunk 或终态 Flush
		// 将返回同一个错误并让生成流程进入失败收尾。
		_ = s.flush(ctx, timerID)
	})
}

func (s *bufferedTextSink) stopTimerLocked() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	// 使已经触发但仍等待锁的旧回调失效。
	s.timerID++
}

var _ TextSink = (*bufferedTextSink)(nil)
