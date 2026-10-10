package agentcontext

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/cloudwego/eino/schema"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

var (
	ErrUnknownContextWindow = errors.New("agent context: unknown context window for model")
	ErrBudgetExceedsWindow  = errors.New("agent context: budget configuration exceeds context window")
)

// EnforcedBudgetPolicy enforces final model budget before each call.
type EnforcedBudgetPolicy struct {
	config       BudgetConfig
	modelWindows map[string]int // modelID -> context window tokens
}

// NewEnforcedBudgetPolicy creates a budget policy that enforces model windows.
func NewEnforcedBudgetPolicy(config BudgetConfig, modelWindows map[string]int) *EnforcedBudgetPolicy {
	return &EnforcedBudgetPolicy{
		config:       config,
		modelWindows: modelWindows,
	}
}

func (p *EnforcedBudgetPolicy) Plan(ctx context.Context, input chat.ContextInput) (BudgetPlan, error) {
	if p == nil {
		return BudgetPlan{}, errors.New("agent context: budget policy is nil")
	}

	// Resolve model window
	// TODO: extract model ID from ContextInput once available
	modelID := "default"
	window := p.modelWindows[modelID]
	if window == 0 {
		return BudgetPlan{}, fmt.Errorf("%w: %s", ErrUnknownContextWindow, modelID)
	}

	plan := BudgetPlan{
		ContextWindowTokens:     window,
		InstructionTokens:       p.config.InstructionTokens,
		SummaryTokens:           p.config.SummaryTokens,
		HistoryTokens:           p.config.HistoryTokens,
		InterviewMaterialTokens: p.config.InterviewMaterialTokens,
		ToolResultTokens:        p.config.ToolResultTokens,
		OutputReservedTokens:    p.config.OutputReservedTokens,
	}

	// Validate total doesn't exceed window
	total := plan.InstructionTokens + plan.SummaryTokens + plan.HistoryTokens +
		plan.InterviewMaterialTokens + plan.ToolResultTokens + plan.OutputReservedTokens

	if total > window {
		return BudgetPlan{}, fmt.Errorf("%w: %d > %d", ErrBudgetExceedsWindow, total, window)
	}

	return plan, nil
}

var _ BudgetPolicy = (*EnforcedBudgetPolicy)(nil)

// CharBasedTokenEstimator provides conservative token estimation based on character count.
type CharBasedTokenEstimator struct {
	charsPerToken float64
}

// NewCharBasedTokenEstimator creates an estimator with the given chars-per-token ratio.
// Common values: 4.0 for English, 2.0 for Chinese.
func NewCharBasedTokenEstimator(charsPerToken float64) *CharBasedTokenEstimator {
	if charsPerToken <= 0 {
		charsPerToken = 4.0 // default to English ratio
	}
	return &CharBasedTokenEstimator{charsPerToken: charsPerToken}
}

func (e *CharBasedTokenEstimator) CountText(model string, text string) (int, error) {
	if e == nil {
		return 0, errors.New("agent context: token estimator is nil")
	}
	chars := len(text)
	tokens := int(math.Ceil(float64(chars) / e.charsPerToken))
	return tokens, nil
}

func (e *CharBasedTokenEstimator) CountMessages(model string, messages []*schema.Message, tools []*schema.ToolInfo) (int, error) {
	if e == nil {
		return 0, errors.New("agent context: token estimator is nil")
	}

	totalChars := 0

	for _, msg := range messages {
		totalChars += len(msg.Role) + 2 // role + ": "
		totalChars += len(msg.Content)
		totalChars += 1 // newline
	}

	// Add tool definitions
	for _, tool := range tools {
		totalChars += len(tool.Name) + len(tool.Desc) + 10 // rough estimate
	}

	tokens := int(math.Ceil(float64(totalChars) / e.charsPerToken))
	return tokens, nil
}

var _ TokenEstimator = (*CharBasedTokenEstimator)(nil)

// BoundarySelector selects a legal compaction prefix from a projection snapshot.
type BoundarySelector interface {
	SelectPrefix(context.Context, BoundaryInput) (BoundaryResult, error)
}

// BoundaryInput provides the snapshot and constraints for boundary selection.
type BoundaryInput struct {
	Snapshot        ProjectionSnapshot
	BaseEntryID     conversation.EntryID
	AvailableTokens int
	CurrentEstimate int
	Estimator       TokenEstimator
}

// ProjectionSnapshot is a simplified view for boundary selection.
type ProjectionSnapshot struct {
	Messages []conversation.ContextMessage
}

// BoundaryResult reports the selected prefix and kept suffix.
type BoundaryResult struct {
	PrefixMessages    []conversation.ContextMessage
	KeptMessages      []conversation.ContextMessage
	FirstKeptEntryID  conversation.EntryID
	PrefixTokens      int
	KeptTokens        int
	NeedsCompaction   bool
	TruncationReason  string
}

// DefaultBoundarySelector selects compaction boundaries by complete user turns
// and complete assistant tool-call groups.
type DefaultBoundarySelector struct {
	estimator TokenEstimator
}

// NewDefaultBoundarySelector creates a boundary selector with the given estimator.
func NewDefaultBoundarySelector(estimator TokenEstimator) *DefaultBoundarySelector {
	return &DefaultBoundarySelector{estimator: estimator}
}

func (s *DefaultBoundarySelector) SelectPrefix(ctx context.Context, input BoundaryInput) (BoundaryResult, error) {
	if s == nil || s.estimator == nil {
		return BoundaryResult{}, errors.New("agent context: boundary selector or estimator is nil")
	}

	// If within budget, no compaction needed
	if input.CurrentEstimate <= input.AvailableTokens {
		return BoundaryResult{
			KeptMessages:      input.Snapshot.Messages,
			FirstKeptEntryID:  "",
			PrefixTokens:      0,
			KeptTokens:        input.CurrentEstimate,
			NeedsCompaction:   false,
			TruncationReason:  "",
		}, nil
	}

	// Group messages by complete turns
	groups := s.groupByCompleteTurns(input.Snapshot.Messages)

	var prefix, kept []conversation.ContextMessage
	var prefixTokens int

	// First pass: find where we must start keeping (base or pending tools)
	var mustKeepFromIndex int = len(groups) // default: can compact all
	for i, group := range groups {
		if input.BaseEntryID != "" && s.isAfterBase(group, input.BaseEntryID) {
			mustKeepFromIndex = i
			break
		}
		if s.hasPendingToolCalls(group) {
			mustKeepFromIndex = i
			break
		}
	}

	// If we must keep everything from the start, check if it fits
	if mustKeepFromIndex == 0 {
		// All content must be kept - check if it fits in budget
		if input.CurrentEstimate > input.AvailableTokens {
			return BoundaryResult{}, fmt.Errorf("no compactable prefix: indivisible content exceeds budget (kept=%d > budget=%d)", input.CurrentEstimate, input.AvailableTokens)
		}
		return BoundaryResult{
			KeptMessages:      input.Snapshot.Messages,
			FirstKeptEntryID:  s.firstEntryID(groups[0]),
			PrefixTokens:      0,
			KeptTokens:        input.CurrentEstimate,
			NeedsCompaction:   false,
			TruncationReason:  "all content must be kept",
		}, nil
	}

	// Calculate tokens for the kept suffix (from mustKeepFromIndex onwards)
	var keptTokens int
	for i := mustKeepFromIndex; i < len(groups); i++ {
		groupTokens, err := s.estimateGroupTokens(groups[i])
		if err != nil {
			return BoundaryResult{}, fmt.Errorf("estimate group tokens: %w", err)
		}
		keptTokens += groupTokens
	}

	// If kept suffix exceeds budget, content is indivisible
	if keptTokens > input.AvailableTokens {
		return BoundaryResult{}, fmt.Errorf("no compactable prefix: indivisible content exceeds budget (kept=%d > budget=%d)", keptTokens, input.AvailableTokens)
	}

	// Second pass: compact as many groups as possible from the front
	for i := 0; i < mustKeepFromIndex; i++ {
		groupTokens, err := s.estimateGroupTokens(groups[i])
		if err != nil {
			return BoundaryResult{}, fmt.Errorf("estimate group tokens: %w", err)
		}

		// Check if keeping this group (not compacting it) would exceed budget
		// keptTokens already includes mustKeepFromIndex onwards
		// If we DON'T compact this group, total kept = keptTokens + groupTokens
		if keptTokens+groupTokens > input.AvailableTokens {
			// Must compact this group to stay within budget
			prefix = append(prefix, groups[i]...)
			prefixTokens += groupTokens
		} else {
			// Keeping this group would still fit in budget, must keep it and all following
			kept = append(kept, s.flattenGroups(groups[i:])...)
			keptTokens = input.CurrentEstimate - prefixTokens
			firstKept := s.firstEntryID(groups[i])
			return s.buildResult(prefix, kept, firstKept, prefixTokens, keptTokens, input.AvailableTokens)
		}
	}

	// All groups before mustKeepFromIndex were compacted
	// kept suffix starts from mustKeepFromIndex
	if mustKeepFromIndex < len(groups) {
		kept = s.flattenGroups(groups[mustKeepFromIndex:])
		firstKept := s.firstEntryID(groups[mustKeepFromIndex])
		return s.buildResult(prefix, kept, firstKept, prefixTokens, keptTokens, input.AvailableTokens)
	}

	// All groups were compacted (mustKeepFromIndex == len(groups))
	if len(prefix) > 0 {
		return BoundaryResult{
			PrefixMessages:   prefix,
			KeptMessages:     nil,
			FirstKeptEntryID: "",
			PrefixTokens:     prefixTokens,
			KeptTokens:       0,
			NeedsCompaction:  true,
		}, nil
	}

	// No compactable prefix found (all content must be kept but exceeds budget)
	return BoundaryResult{}, errors.New("no compactable prefix: indivisible content exceeds budget")
}

func (s *DefaultBoundarySelector) groupByCompleteTurns(messages []conversation.ContextMessage) [][]conversation.ContextMessage {
	var groups [][]conversation.ContextMessage
	var currentGroup []conversation.ContextMessage

	for _, msg := range messages {
		currentGroup = append(currentGroup, msg)

		// A turn is complete when we see:
		// 1. A user message (starts new turn)
		// 2. An assistant message with no tool calls
		// 3. An assistant message where all tool calls have results
		if msg.Role == "user" {
			if len(currentGroup) > 1 {
				// Complete previous group before starting new turn
				groups = append(groups, currentGroup[:len(currentGroup)-1])
				currentGroup = []conversation.ContextMessage{msg}
			}
		} else if msg.Role == "assistant" {
			if s.isCompleteTurn(currentGroup) {
				groups = append(groups, currentGroup)
				currentGroup = nil
			}
		}
	}

	if len(currentGroup) > 0 {
		groups = append(groups, currentGroup)
	}

	return groups
}

func (s *DefaultBoundarySelector) isCompleteTurn(group []conversation.ContextMessage) bool {
	// Find the last assistant message
	var lastAssistant *conversation.ContextMessage
	for i := len(group) - 1; i >= 0; i-- {
		if group[i].Role == "assistant" {
			lastAssistant = &group[i]
			break
		}
	}

	if lastAssistant == nil {
		return false
	}

	// Check if it has tool calls
	toolCallCount := strings.Count(lastAssistant.Content, "Tool call:")
	if toolCallCount == 0 {
		return true // No tool calls, turn is complete
	}

	// Count tool results after this assistant
	toolResultCount := 0
	for _, msg := range group {
		if strings.Contains(msg.Content, "Tool result:") {
			toolResultCount++
		}
	}

	// Turn is complete if all tool calls have results
	return toolResultCount >= toolCallCount
}

func (s *DefaultBoundarySelector) hasPendingToolCalls(group []conversation.ContextMessage) bool {
	return !s.isCompleteTurn(group)
}

func (s *DefaultBoundarySelector) isAfterBase(group []conversation.ContextMessage, baseID conversation.EntryID) bool {
	if baseID == "" {
		return false
	}
	for _, msg := range group {
		for _, src := range msg.Sources {
			// Check if this entry is at or after the base
			if src.EntryID >= baseID {
				return true
			}
		}
	}
	return false
}

func (s *DefaultBoundarySelector) firstEntryID(group []conversation.ContextMessage) conversation.EntryID {
	if len(group) == 0 || len(group[0].Sources) == 0 {
		return ""
	}
	return group[0].Sources[0].EntryID
}

func (s *DefaultBoundarySelector) flattenGroups(groups [][]conversation.ContextMessage) []conversation.ContextMessage {
	var result []conversation.ContextMessage
	for _, group := range groups {
		result = append(result, group...)
	}
	return result
}

func (s *DefaultBoundarySelector) estimateGroupTokens(group []conversation.ContextMessage) (int, error) {
	totalChars := 0
	for _, msg := range group {
		totalChars += len(msg.Role) + 2 + len(msg.Content) + 1
	}
	tokens := int(math.Ceil(float64(totalChars) / 4.0)) // Use default 4.0 ratio
	return tokens, nil
}

func (s *DefaultBoundarySelector) buildResult(prefix, kept []conversation.ContextMessage, firstKept conversation.EntryID, prefixTokens, keptTokens, budget int) (BoundaryResult, error) {
	var reason string
	if keptTokens > budget {
		reason = fmt.Sprintf("kept suffix exceeds budget: %d > %d", keptTokens, budget)
	}

	return BoundaryResult{
		PrefixMessages:    prefix,
		KeptMessages:      kept,
		FirstKeptEntryID:  firstKept,
		PrefixTokens:      prefixTokens,
		KeptTokens:        keptTokens,
		NeedsCompaction:   true,
		TruncationReason:  reason,
	}, nil
}

var _ BoundarySelector = (*DefaultBoundarySelector)(nil)
