package conversation

import (
	"fmt"
	"strings"

	"interview-memory-agent/backend/internal/domain/domainerr"
)

type DeclaredToolCall struct {
	ID   string
	Name string
}

func extractDeclaredCalls(msg EntryMessage) []DeclaredToolCall {
	var calls []DeclaredToolCall
	for _, part := range msg.Content {
		if part.Type == EntryPartToolCall && part.ToolCall != nil {
			calls = append(calls, DeclaredToolCall{
				ID:   part.ToolCall.ID(),
				Name: part.ToolCall.Name(),
			})
		}
	}
	return calls
}

func ValidateEntryMessage(message EntryMessage) error {
	switch message.Role {
	case EntryRoleUser:
		if message.ToolResult != nil {
			return fmt.Errorf("%w: user message cannot have toolResult", domainerr.ErrInvalidInput)
		}
		if message.StopReason != "" {
			return fmt.Errorf("%w: user message cannot have stopReason", domainerr.ErrInvalidInput)
		}
		if len(message.Content) == 0 {
			return fmt.Errorf("%w: user message content cannot be empty", domainerr.ErrInvalidInput)
		}
		for i, part := range message.Content {
			if part.Type != EntryPartText {
				return fmt.Errorf("%w: user message part %d must be text", domainerr.ErrInvalidInput, i)
			}
			if part.Text == nil || strings.TrimSpace(*part.Text) == "" {
				return fmt.Errorf("%w: user message part %d text cannot be empty", domainerr.ErrInvalidInput, i)
			}
			if part.ToolCall != nil {
				return fmt.Errorf("%w: user message part %d cannot contain toolCall", domainerr.ErrInvalidInput, i)
			}
		}

	case EntryRoleAssistant:
		if message.ToolResult != nil {
			return fmt.Errorf("%w: assistant message cannot have toolResult", domainerr.ErrInvalidInput)
		}
		if len(message.Content) == 0 {
			return fmt.Errorf("%w: assistant message content cannot be empty", domainerr.ErrInvalidInput)
		}
		seenCallIDs := make(map[string]bool)
		hasToolCall := false

		for i, part := range message.Content {
			switch part.Type {
			case EntryPartText:
				if part.Text == nil {
					return fmt.Errorf("%w: assistant text part %d text cannot be nil", domainerr.ErrInvalidInput, i)
				}
				if part.ToolCall != nil {
					return fmt.Errorf("%w: assistant text part %d cannot contain toolCall", domainerr.ErrInvalidInput, i)
				}
			case EntryPartToolCall:
				if part.ToolCall == nil {
					return fmt.Errorf("%w: assistant toolCall part %d toolCall cannot be nil", domainerr.ErrInvalidInput, i)
				}
				if part.Text != nil {
					return fmt.Errorf("%w: assistant toolCall part %d cannot contain text", domainerr.ErrInvalidInput, i)
				}
				if err := ValidateSafeToolCall(*part.ToolCall); err != nil {
					return err
				}
				cid := part.ToolCall.ID()
				if seenCallIDs[cid] {
					return fmt.Errorf("%w: duplicate tool call ID %q in assistant message", domainerr.ErrInvalidInput, cid)
				}
				seenCallIDs[cid] = true
				hasToolCall = true
			default:
				return fmt.Errorf("%w: invalid assistant part type %q", domainerr.ErrInvalidInput, part.Type)
			}
		}

		if hasToolCall && message.StopReason != EntryStopTools {
			return fmt.Errorf("%w: assistant message with tool calls must have stopReason %q, got %q", domainerr.ErrInvalidInput, EntryStopTools, message.StopReason)
		}
		if !hasToolCall && message.StopReason != EntryStopNormal {
			return fmt.Errorf("%w: assistant message without tool calls must have stopReason %q, got %q", domainerr.ErrInvalidInput, EntryStopNormal, message.StopReason)
		}

	case EntryRoleToolResult:
		if len(message.Content) != 0 {
			return fmt.Errorf("%w: toolResult message cannot contain content parts", domainerr.ErrInvalidInput)
		}
		if message.StopReason != "" {
			return fmt.Errorf("%w: toolResult message cannot have stopReason", domainerr.ErrInvalidInput)
		}
		if message.ToolResult == nil {
			return fmt.Errorf("%w: toolResult message must contain ToolResult", domainerr.ErrInvalidInput)
		}
		if strings.TrimSpace(string(message.ToolResult.CallEntryID)) == "" {
			return fmt.Errorf("%w: toolResult CallEntryID cannot be empty", domainerr.ErrInvalidInput)
		}
		if strings.TrimSpace(message.ToolResult.CallID) == "" {
			return fmt.Errorf("%w: toolResult CallID cannot be empty", domainerr.ErrInvalidInput)
		}
		if strings.TrimSpace(message.ToolResult.ToolName) == "" {
			return fmt.Errorf("%w: toolResult ToolName cannot be empty", domainerr.ErrInvalidInput)
		}
		if err := ValidateSafeToolResult(message.ToolResult.ToolName, message.ToolResult.Result); err != nil {
			return err
		}

	default:
		return fmt.Errorf("%w: unknown entry role %q", domainerr.ErrInvalidInput, message.Role)
	}

	return nil
}

func ValidateToolContinuation(parentPath []ConversationEntry, next EntryMessage) error {
	if err := ValidatePathConsistency(parentPath); err != nil {
		return err
	}
	if err := ValidateEntryMessage(next); err != nil {
		return err
	}

	if len(parentPath) == 0 {
		if next.Role != EntryRoleUser {
			return fmt.Errorf("%w: first root entry must have role %q, got %q", domainerr.ErrInvalidInput, EntryRoleUser, next.Role)
		}
		return nil
	}

	// Check call ID uniqueness across the root-to-leaf path
	if next.Role == EntryRoleAssistant {
		pathCallIDs := make(map[string]bool)
		for _, entry := range parentPath {
			if entry.Message.Role == EntryRoleAssistant {
				for _, call := range extractDeclaredCalls(entry.Message) {
					pathCallIDs[call.ID] = true
				}
			}
		}
		for _, call := range extractDeclaredCalls(next) {
			if pathCallIDs[call.ID] {
				return fmt.Errorf("%w: tool call ID %q already exists in path", domainerr.ErrConflict, call.ID)
			}
		}
	}

	parent := parentPath[len(parentPath)-1]

	switch parent.Message.Role {
	case EntryRoleUser:
		if next.Role == EntryRoleToolResult {
			return fmt.Errorf("%w: tool result cannot follow user message", domainerr.ErrInvalidInput)
		}
		return nil

	case EntryRoleAssistant:
		declaredCalls := extractDeclaredCalls(parent.Message)
		if len(declaredCalls) > 0 {
			if next.Role != EntryRoleToolResult {
				return fmt.Errorf("%w: assistant has pending tool calls, expected toolResult but got %q", domainerr.ErrConflict, next.Role)
			}
			expectedCall := declaredCalls[0]
			if next.ToolResult.CallEntryID != parent.ID {
				return fmt.Errorf("%w: toolResult callEntryId %q does not match parent assistant ID %q", domainerr.ErrConflict, next.ToolResult.CallEntryID, parent.ID)
			}
			if next.ToolResult.CallID != expectedCall.ID {
				return fmt.Errorf("%w: toolResult callId %q does not match expected %q", domainerr.ErrConflict, next.ToolResult.CallID, expectedCall.ID)
			}
			if next.ToolResult.ToolName != expectedCall.Name {
				return fmt.Errorf("%w: toolResult toolName %q does not match expected %q", domainerr.ErrConflict, next.ToolResult.ToolName, expectedCall.Name)
			}
			return nil
		}

		if next.Role == EntryRoleToolResult {
			return fmt.Errorf("%w: orphan toolResult following assistant with no tool calls", domainerr.ErrInvalidInput)
		}
		return nil

	case EntryRoleToolResult:
		asstEntry, declaredCalls, results, isClosed, err := validateToolGroup(parentPath)
		if err != nil {
			return err
		}
		k := len(results)
		N := len(declaredCalls)

		if !isClosed {
			if next.Role != EntryRoleToolResult {
				return fmt.Errorf("%w: unclosed tool group: expected toolResult %d/%d, got %q", domainerr.ErrConflict, k+1, N, next.Role)
			}
			expectedCall := declaredCalls[k]
			if next.ToolResult.CallEntryID != asstEntry.ID {
				return fmt.Errorf("%w: toolResult callEntryId %q does not match assistant ID %q", domainerr.ErrConflict, next.ToolResult.CallEntryID, asstEntry.ID)
			}
			if next.ToolResult.CallID != expectedCall.ID {
				return fmt.Errorf("%w: toolResult callId %q does not match expected %q", domainerr.ErrConflict, next.ToolResult.CallID, expectedCall.ID)
			}
			if next.ToolResult.ToolName != expectedCall.Name {
				return fmt.Errorf("%w: toolResult toolName %q does not match expected %q", domainerr.ErrConflict, next.ToolResult.ToolName, expectedCall.Name)
			}
			return nil
		}

		// isClosed == true
		if next.Role == EntryRoleToolResult {
			return fmt.Errorf("%w: orphan or duplicate toolResult following completed tool group", domainerr.ErrConflict)
		}
		return nil

	default:
		return fmt.Errorf("%w: unknown parent role %q", domainerr.ErrInvalidInput, parent.Message.Role)
	}
}

func validateToolGroup(path []ConversationEntry) (asstEntry *ConversationEntry, declaredCalls []DeclaredToolCall, results []ConversationEntry, isClosed bool, err error) {
	if len(path) == 0 {
		return nil, nil, nil, true, nil
	}

	var resultsRev []ConversationEntry
	asstIdx := -1
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Message.Role == EntryRoleToolResult {
			resultsRev = append(resultsRev, path[i])
		} else if path[i].Message.Role == EntryRoleAssistant {
			asstIdx = i
			break
		} else {
			return nil, nil, nil, false, fmt.Errorf("%w: unexpected role %q before toolResult group", ErrEntryCorrupt, path[i].Message.Role)
		}
	}
	if asstIdx == -1 {
		return nil, nil, nil, false, fmt.Errorf("%w: no assistant found for toolResult group", ErrEntryCorrupt)
	}

	asst := &path[asstIdx]
	declaredCalls = extractDeclaredCalls(asst.Message)
	N := len(declaredCalls)
	k := len(resultsRev)

	if N == 0 {
		return nil, nil, nil, false, fmt.Errorf("%w: assistant has no tool calls but %d tool results follow", ErrEntryCorrupt, k)
	}
	if k > N {
		return nil, nil, nil, false, fmt.Errorf("%w: more tool results (%d) than declared calls (%d)", ErrEntryCorrupt, k, N)
	}

	// Reverse resultsRev to declaration order in O(k)
	results = make([]ConversationEntry, k)
	for i := 0; i < k; i++ {
		results[i] = resultsRev[k-1-i]
	}

	// Thoroughly verify each result matches declared call in ID, toolName, callEntryId
	for i := 0; i < k; i++ {
		r := results[i].Message.ToolResult
		if r == nil {
			return nil, nil, nil, false, fmt.Errorf("%w: missing toolResult data at position %d", ErrEntryCorrupt, i)
		}
		if r.CallEntryID != asst.ID {
			return nil, nil, nil, false, fmt.Errorf("%w: toolResult callEntryId %q != assistant ID %q at position %d", ErrEntryCorrupt, r.CallEntryID, asst.ID, i)
		}
		if r.CallID != declaredCalls[i].ID {
			return nil, nil, nil, false, fmt.Errorf("%w: toolResult callId %q != expected %q at position %d", ErrEntryCorrupt, r.CallID, declaredCalls[i].ID, i)
		}
		if r.ToolName != declaredCalls[i].Name {
			return nil, nil, nil, false, fmt.Errorf("%w: toolResult toolName %q != expected %q at position %d", ErrEntryCorrupt, r.ToolName, declaredCalls[i].Name, i)
		}
	}

	return asst, declaredCalls, results, k == N, nil
}

// ValidatePathConsistency checks historical relationships, not the full message
// payloads (the repository codec validates those). In particular, minimal user
// and plain-assistant fixtures remain supported by the branch-point helper.
// Each entry and declared call is visited once; call IDs are scoped to this path.
func ValidatePathConsistency(path []ConversationEntry) error {
	if len(path) > 0 && path[0].Message.Role != EntryRoleUser {
		return fmt.Errorf("%w: root entry %q must be user", ErrEntryCorrupt, path[0].ID)
	}

	seenCallIDs := make(map[string]bool)
	var pending []DeclaredToolCall
	var callEntryID EntryID
	for i, entry := range path {
		msg := entry.Message
		if len(pending) > 0 {
			if msg.Role != EntryRoleToolResult || msg.ToolResult == nil {
				return fmt.Errorf("%w: expected toolResult at path index %d", ErrEntryCorrupt, i)
			}
			r := msg.ToolResult
			if r.CallEntryID != callEntryID || r.CallID != pending[0].ID || r.ToolName != pending[0].Name {
				return fmt.Errorf("%w: corrupted toolResult at path index %d", ErrEntryCorrupt, i)
			}
			pending = pending[1:]
			continue
		}

		switch msg.Role {
		case EntryRoleUser:
			// Content validation belongs to ValidateEntryMessage, not path validation.
		case EntryRoleAssistant:
			var calls []DeclaredToolCall
			for _, part := range msg.Content {
				if part.Type != EntryPartToolCall {
					if part.ToolCall != nil {
						return fmt.Errorf("%w: misplaced tool call at path index %d", ErrEntryCorrupt, i)
					}
					continue
				}
				if part.ToolCall == nil || part.Text != nil {
					return fmt.Errorf("%w: malformed declared call at path index %d", ErrEntryCorrupt, i)
				}
				call := DeclaredToolCall{ID: part.ToolCall.ID(), Name: part.ToolCall.Name()}
				if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" {
					return fmt.Errorf("%w: empty declared identity at path index %d", ErrEntryCorrupt, i)
				}
				if seenCallIDs[call.ID] {
					return fmt.Errorf("%w: reused tool call ID at path index %d", ErrEntryCorrupt, i)
				}
				seenCallIDs[call.ID] = true
				calls = append(calls, call)
			}
			if (len(calls) > 0) != (msg.StopReason == EntryStopTools) {
				return fmt.Errorf("%w: inconsistent tool stop reason at path index %d", ErrEntryCorrupt, i)
			}
			pending = calls
			callEntryID = entry.ID
		case EntryRoleToolResult:
			return fmt.Errorf("%w: orphan or extra toolResult at path index %d", ErrEntryCorrupt, i)
		default:
			return fmt.Errorf("%w: unknown role at path index %d", ErrEntryCorrupt, i)
		}
	}
	// An unfinished group is valid only at the leaf, while tools are pending.
	return nil
}

func IsClosedBranchPoint(path []ConversationEntry) bool {
	if err := ValidatePathConsistency(path); err != nil {
		return false
	}
	if len(path) == 0 {
		return true
	}
	last := path[len(path)-1]
	switch last.Message.Role {
	case EntryRoleUser:
		return true
	case EntryRoleAssistant:
		calls := extractDeclaredCalls(last.Message)
		return len(calls) == 0
	case EntryRoleToolResult:
		_, _, _, isClosed, err := validateToolGroup(path)
		if err != nil {
			return false
		}
		return isClosed
	default:
		return false
	}
}
