// Package eino provides type conversions between Eino runtime schema
// and the conversation domain model for persistent fact recording.
package eino

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// ConvertAssistantMessage converts a complete Eino assistant message with
// text and/or tool calls into a persistent EntryMessage. It requires a
// registered tool policy for every tool call and fails immediately if any
// policy is missing or any projection fails.
//
// The caller must ensure the message is complete (not a streaming delta)
// and must have already validated the tool iteration limit.
func ConvertAssistantMessage(msg *schema.Message) (conversation.EntryMessage, error) {
	if msg == nil {
		return conversation.EntryMessage{}, errors.New("eino converter: message is required")
	}
	if msg.Role != schema.Assistant {
		return conversation.EntryMessage{}, fmt.Errorf("eino converter: expected assistant role, got %s", msg.Role)
	}

	hasText := msg.Content != ""
	hasToolCalls := len(msg.ToolCalls) > 0

	if !hasText && !hasToolCalls {
		return conversation.EntryMessage{}, errors.New("eino converter: assistant message must have content or tool calls")
	}

	var parts []conversation.EntryPart

	// Text content always comes first if present
	if hasText {
		text := msg.Content
		parts = append(parts, conversation.EntryPart{
			Type: conversation.EntryPartText,
			Text: &text,
		})
	}

	// Tool calls follow text in declaration order
	if hasToolCalls {
		for i := range msg.ToolCalls {
			tc := &msg.ToolCalls[i]
			safeCall, err := projectToolCall(tc)
			if err != nil {
				return conversation.EntryMessage{}, fmt.Errorf("project tool call %s: %w", tc.Function.Name, err)
			}
			parts = append(parts, conversation.EntryPart{
				Type:     conversation.EntryPartToolCall,
				ToolCall: &safeCall,
			})
		}
	}

	stopReason := conversation.EntryStopNormal
	if hasToolCalls {
		stopReason = conversation.EntryStopTools
	}

	return conversation.EntryMessage{
		Role:       conversation.EntryRoleAssistant,
		Content:    parts,
		StopReason: stopReason,
	}, nil
}

// ConvertToolResultMessages converts a batch of tool result messages into
// EntryMessages. Each result must reference a tool call ID from the most
// recently recorded assistant message. The caller is responsible for:
//   - Validating result count matches pending tool calls
//   - Verifying each result's ToolCallID exists in the pending set
//   - Ensuring no duplicate or missing results
//   - Providing the CallEntryID of the assistant message that requested these tools
//
// This function only handles the type conversion and safe projection.
func ConvertToolResultMessages(
	results []*schema.Message,
	callEntryID conversation.EntryID,
	pendingCalls map[string]string, // callID -> toolName
) ([]conversation.EntryMessage, error) {
	if len(results) == 0 {
		return nil, errors.New("eino converter: tool results cannot be empty")
	}
	if callEntryID == "" {
		return nil, errors.New("eino converter: call entry ID is required")
	}
	if len(pendingCalls) == 0 {
		return nil, errors.New("eino converter: pending calls map cannot be empty")
	}

	entryMessages := make([]conversation.EntryMessage, 0, len(results))

	for _, result := range results {
		if result == nil {
			return nil, errors.New("eino converter: result message cannot be nil")
		}
		if result.Role != schema.Tool {
			return nil, fmt.Errorf("eino converter: expected tool role, got %s", result.Role)
		}
		if result.ToolCallID == "" {
			return nil, errors.New("eino converter: tool call ID is required in result")
		}

		toolName, exists := pendingCalls[result.ToolCallID]
		if !exists {
			return nil, fmt.Errorf("eino converter: tool call ID %s not in pending set", result.ToolCallID)
		}

		// Determine if this is an error result
		// Eino may populate result.Content with error text or success content
		// For now, we treat any non-empty content as success
		// Error detection may need refinement based on actual Eino conventions
		isError := false
		rawResult := result.Content

		safeResult, err := projectToolResult(toolName, rawResult, isError)
		if err != nil {
			return nil, fmt.Errorf("project tool result %s (call %s): %w", toolName, result.ToolCallID, err)
		}

		entryMessages = append(entryMessages, conversation.EntryMessage{
			Role: conversation.EntryRoleToolResult,
			ToolResult: &conversation.EntryToolResult{
				CallEntryID: callEntryID,
				CallID:      result.ToolCallID,
				ToolName:    toolName,
				IsError:     isError,
				Result:      safeResult,
			},
		})
	}

	return entryMessages, nil
}

// projectToolCall applies the registered tool policy to extract safe fields
// from the raw Eino tool call arguments.
func projectToolCall(tc *schema.ToolCall) (conversation.SafeToolCall, error) {
	if tc == nil {
		return conversation.SafeToolCall{}, errors.New("tool call is required")
	}
	if tc.ID == "" {
		return conversation.SafeToolCall{}, errors.New("tool call ID is required")
	}
	if tc.Function.Name == "" {
		return conversation.SafeToolCall{}, errors.New("tool call function name is required")
	}

	// Look up the registered policy
	// For now, we assume version "1" as the default
	// TODO: Allow runtime to specify policy version per tool
	policyVersion := "1"
	policy, exists := conversation.GetToolPolicy(tc.Function.Name, policyVersion)
	if !exists {
		return conversation.SafeToolCall{}, fmt.Errorf("no registered policy for tool %s@%s", tc.Function.Name, policyVersion)
	}

	// Parse arguments as JSON
	var rawArgs json.RawMessage
	if tc.Function.Arguments != "" {
		rawArgs = json.RawMessage(tc.Function.Arguments)
	} else {
		rawArgs = json.RawMessage("{}")
	}

	safeCall, err := conversation.ProjectSafeToolCall(policy, tc.ID, rawArgs)
	if err != nil {
		return conversation.SafeToolCall{}, err
	}

	// Validate the projected call
	if err := conversation.ValidateSafeToolCall(safeCall); err != nil {
		return conversation.SafeToolCall{}, err
	}

	return safeCall, nil
}

// projectToolResult applies the registered tool policy to extract safe fields
// from the raw tool result content.
func projectToolResult(toolName, rawContent string, isError bool) (conversation.SafeToolResult, error) {
	if toolName == "" {
		return conversation.SafeToolResult{}, errors.New("tool name is required")
	}

	// Look up the registered policy
	policyVersion := "1"
	policy, exists := conversation.GetToolPolicy(toolName, policyVersion)
	if !exists {
		return conversation.SafeToolResult{}, fmt.Errorf("no registered policy for tool %s@%s", toolName, policyVersion)
	}

	// For error results, we may want to apply different projection rules
	// For now, treat all results uniformly
	var rawResult json.RawMessage
	if rawContent != "" {
		// Assume the result is already JSON
		// If it's plain text, we may need to wrap it
		rawResult = json.RawMessage(rawContent)
	} else {
		rawResult = json.RawMessage("{}")
	}

	safeResult, err := conversation.ProjectSafeToolResult(policy, rawResult)
	if err != nil {
		return conversation.SafeToolResult{}, err
	}

	// Validate the projected result
	if err := conversation.ValidateSafeToolResult(toolName, safeResult); err != nil {
		return conversation.SafeToolResult{}, err
	}

	return safeResult, nil
}
