package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func lifecycleBegin(t *testing.T, ctx context.Context, r *AgentRunRepository, running bool) chat.BeginTurnResult {
	t.Helper()
	turn, err := r.BeginTurn(ctx, runInput("lifecycle"))
	if err != nil {
		t.Fatal(err)
	}
	if running {
		run, err := r.MarkRunning(ctx, turn.Run.ID)
		if err != nil {
			t.Fatal(err)
		}
		turn.Run = &run
	}
	return turn
}

func lifecycleToken(t *testing.T, run agentrun.Run) conversation.HeadToken {
	t.Helper()
	token, err := run.ActiveHeadToken()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func lifecycleCall(t *testing.T) conversation.EntryMessage {
	t.Helper()
	policy := conversation.ToolPersistencePolicy{
		ToolName: "lifecycle-search", Version: "v1",
		ArgumentFields:  []conversation.ProjectionFieldRule{{SourceKey: "q", StoredKey: "q", MaxBytes: 40}},
		ResultFields:    []conversation.ProjectionFieldRule{{SourceKey: "answer", StoredKey: "answer", MaxBytes: 40}},
		MaxPreviewBytes: 30,
	}
	if _, exists := conversation.GetToolPolicy(policy.ToolName, policy.Version); !exists {
		if err := conversation.RegisterToolPolicy(policy); err != nil {
			t.Fatal(err)
		}
	}
	message := conversation.EntryMessage{Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopTools}
	for _, id := range []string{"c1", "c2"} {
		call, err := conversation.ProjectSafeToolCall(policy, id, json.RawMessage(`{"q":"approved","undeclared":"must disappear"}`))
		if err != nil {
			t.Fatal(err)
		}
		message.Content = append(message.Content, conversation.EntryPart{Type: conversation.EntryPartToolCall, ToolCall: &call})
	}
	return message
}

func lifecycleResults(t *testing.T, callID conversation.EntryID) []conversation.EntryMessage {
	t.Helper()
	policy, ok := conversation.GetToolPolicy("lifecycle-search", "v1")
	if !ok {
		t.Fatal("missing policy")
	}
	result, err := conversation.ProjectSafeToolResult(policy, json.RawMessage(`{"answer":"approved","undeclared":"must disappear"}`))
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately reverse completion order. Error results still close a call.
	var messages []conversation.EntryMessage
	for _, id := range []string{"c2", "c1"} {
		messages = append(messages, conversation.EntryMessage{Role: conversation.EntryRoleToolResult,
			ToolResult: &conversation.EntryToolResult{CallEntryID: callID, CallID: id, ToolName: policy.ToolName, Result: result.Clone(), IsError: id == "c2"}})
	}
	return messages
}

func lifecycleCommitCall(t *testing.T, ctx context.Context, r *AgentRunRepository, run agentrun.Run) chat.CommitRunMessagesResult {
	t.Helper()
	result, err := r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{RunID: run.ID, ExpectedHead: lifecycleToken(t, run), Messages: []conversation.EntryMessage{lifecycleCall(t)}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func lifecycleFinish(t *testing.T, run agentrun.Run, status conversation.MessageStatus) chat.FinishAssistantInput {
	t.Helper()
	token := lifecycleToken(t, run)
	input := chat.FinishAssistantInput{AssistantMessageID: run.AssistantMessageID, Status: status, ExpectedHead: &token}
	if status == conversation.MessageStatusCompleted {
		final := entryTxMessage(conversation.EntryRoleAssistant)
		input.FinalMessage = &final
	}
	if status == conversation.MessageStatusFailed {
		input.ErrorCode, input.ErrorMessage = "model_failed", "生成失败，请重试。"
	}
	return input
}

// Snapshot every affected table, including clocks and immutable facts. A failed
// transaction must preserve all of them, not just the count or active leaf.
func lifecycleSnapshot(t *testing.T, ctx context.Context, db *storage.DB) string {
	t.Helper()
	var snapshot strings.Builder
	for _, table := range []string{"conversations", "messages", "agent_runs", "conversation_entry_nodes", "conversation_entry_heads"} {
		rows, err := db.QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY 1")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values, ptrs := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			snapshot.WriteString(table)
			snapshot.Write(data)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return snapshot.String()
}

func TestAgentRunLifecycleSuccessfulLoopAndFinal(t *testing.T) {
	db, ctx, r := runFixture(t)
	turn := lifecycleBegin(t, ctx, r, false)
	if err := r.AppendAssistantText(ctx, turn.AssistantMessage.ID, "visible prelude; "); err != nil {
		t.Fatal(err)
	}
	run, err := r.MarkRunning(ctx, turn.Run.ID)
	if err != nil || run.Status != agentrun.StatusRunning || run.StartedAt == nil || run.HeadVersion != turn.Run.HeadVersion {
		t.Fatalf("start = %+v, %v", run, err)
	}
	before := lifecycleSnapshot(t, ctx, db)
	if got, err := r.MarkRunning(ctx, run.ID); !errors.Is(err, domainerr.ErrConflict) || !reflect.DeepEqual(got, agentrun.Run{}) {
		t.Fatalf("duplicate start = %+v, %v", got, err)
	}
	if lifecycleSnapshot(t, ctx, db) != before {
		t.Fatal("duplicate start wrote state")
	}
	call := lifecycleCommitCall(t, ctx, r, run)
	if call.Run.Status != agentrun.StatusWaitingTool || call.Run.LastClosedEntryID != run.UserEntryID || call.Run.LastEntryID != call.Entries[0].ID || call.Head.Version != 2 {
		t.Fatalf("call = %+v", call)
	}
	if err := r.AppendAssistantText(ctx, run.AssistantMessageID, "tool prelude; "); err != nil {
		t.Fatal(err)
	}
	results, err := r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{RunID: run.ID, ExpectedHead: entryTxToken(call.Head), Messages: lifecycleResults(t, call.Entries[0].ID)})
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Entries) != 2 || results.Entries[0].Message.ToolResult.CallID != "c1" || results.Entries[1].Message.ToolResult.CallID != "c2" || !results.Entries[1].Message.ToolResult.IsError ||
		results.Run.Status != agentrun.StatusRunning || results.Run.LastClosedEntryID != results.Run.LastEntryID || results.Head.Version != 4 {
		t.Fatalf("results = %+v", results)
	}
	if err := r.AppendAssistantText(ctx, run.AssistantMessageID, "visible final"); err != nil {
		t.Fatal(err)
	}
	finished, err := r.FinishAssistant(ctx, lifecycleFinish(t, results.Run, conversation.MessageStatusCompleted))
	if err != nil || finished.Status != conversation.MessageStatusCompleted || finished.Content != "visible prelude; tool prelude; visible final" {
		t.Fatalf("finish = %+v, %v", finished, err)
	}
	retry, err := r.BeginTurn(ctx, runInput("lifecycle"))
	if err != nil || retry.Run.FinalEntryID == nil || retry.Run.LastClosedEntryID != *retry.Run.FinalEntryID || retry.Run.HeadVersion != 5 || retry.Run.FinishedAt == nil {
		t.Fatalf("terminal = %+v, %v", retry, err)
	}
	path, err := r.tree.ReadPath(ctx, conversation.PathQuery{ConversationID: run.ConversationID, UseActiveLeaf: true})
	if err != nil || len(path.Entries) != 5 || *path.Entries[4].Message.Content[0].Text != "approved text" {
		t.Fatalf("complete final did not remain distinct from cumulative text: %+v %v", path, err)
	}
	encoded, err := EncodeEntryMessage(path.Entries[1].Message)
	if err != nil || strings.Contains(string(encoded), "undeclared") {
		t.Fatalf("raw call bypass: %s %v", encoded, err)
	}
	assertRunCounts(t, ctx, db, 2, 5, 1, 1)
	if _, err := r.BeginTurn(ctx, runInput("next")); err != nil {
		t.Fatal(err)
	}
	before = lifecycleSnapshot(t, ctx, db)
	// The completed retry must not depend on the old token or validate a
	// new final payload, and must not duplicate its committed final Entry.
	got, err := r.FinishAssistant(ctx, chat.FinishAssistantInput{
		AssistantMessageID: run.AssistantMessageID, Status: conversation.MessageStatusCompleted,
		FinalMessage: &conversation.EntryMessage{}, ErrorCode: "ignored!", ErrorMessage: "ignored",
	})
	if err != nil || !reflect.DeepEqual(got, finished) {
		t.Fatalf("completed retry = %+v %v", got, err)
	}
	if got, err := r.FinishAssistant(ctx, chat.FinishAssistantInput{AssistantMessageID: run.AssistantMessageID, Status: conversation.MessageStatusFailed}); !errors.Is(err, domainerr.ErrConflict) || !reflect.DeepEqual(got, conversation.Message{}) {
		t.Fatalf("different terminal after completed = %+v %v", got, err)
	}
	if lifecycleSnapshot(t, ctx, db) != before {
		t.Fatal("completed retry changed persisted state")
	}
}

func TestAgentRunLifecycleUnsuccessfulFinishPreservesBranch(t *testing.T) {
	for _, stage := range []string{"pending", "running", "waiting", "closed", "second waiting"} {
		for _, status := range []conversation.MessageStatus{conversation.MessageStatusFailed, conversation.MessageStatusCancelled} {
			t.Run(stage+"/"+string(status), func(t *testing.T) {
				db, ctx, r := runFixture(t)
				turn := lifecycleBegin(t, ctx, r, stage != "pending")
				run := *turn.Run
				if stage == "waiting" || stage == "closed" || stage == "second waiting" {
					call := lifecycleCommitCall(t, ctx, r, run)
					run = call.Run
					if stage != "waiting" {
						result, err := r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{RunID: run.ID, ExpectedHead: entryTxToken(call.Head), Messages: lifecycleResults(t, call.Entries[0].ID)})
						if err != nil {
							t.Fatal(err)
						}
						run = result.Run
						if stage == "second waiting" {
							message := lifecycleCall(t)
							for i := range message.Content {
								policy, _ := conversation.GetToolPolicy("lifecycle-search", "v1")
								call, err := conversation.ProjectSafeToolCall(policy, "next-"+message.Content[i].ToolCall.ID(), json.RawMessage(`{"q":"next"}`))
								if err != nil {
									t.Fatal(err)
								}
								message.Content[i].ToolCall = &call
							}
							result, err = r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{RunID: run.ID, ExpectedHead: lifecycleToken(t, run), Messages: []conversation.EntryMessage{message}})
							if err != nil {
								t.Fatal(err)
							}
							run = result.Run
						}
					}
				}
				if err := r.AppendAssistantText(ctx, run.AssistantMessageID, "partial visible"); err != nil {
					t.Fatal(err)
				}
				oldPath, err := r.tree.ReadPath(ctx, conversation.PathQuery{ConversationID: run.ConversationID, UseActiveLeaf: true})
				if err != nil {
					t.Fatal(err)
				}
				input := lifecycleFinish(t, run, status)
				finished, err := r.FinishAssistant(ctx, input)
				if err != nil || finished.Status != status || finished.Content != "partial visible" {
					t.Fatalf("finish = %+v, %v", finished, err)
				}
				retry, err := r.BeginTurn(ctx, runInput("lifecycle"))
				if err != nil {
					t.Fatal(err)
				}
				head, err := r.tree.GetHead(ctx, run.ConversationID)
				if err != nil {
					t.Fatal(err)
				}
				wantVersion := run.HeadVersion
				if run.LastEntryID != run.LastClosedEntryID {
					wantVersion++
				}
				if !sameEntryID(head.ActiveLeafID, &run.LastClosedEntryID) || head.Version != wantVersion || retry.Run.HeadVersion != wantVersion || retry.Run.LastEntryID != run.LastEntryID || retry.Run.FinalEntryID != nil {
					t.Fatalf("rewind = %+v %+v", head, retry.Run)
				}
				branch, err := r.tree.ReadPath(ctx, conversation.PathQuery{ConversationID: run.ConversationID, LeafID: &run.LastEntryID})
				if err != nil || !reflect.DeepEqual(branch.Entries, oldPath.Entries) {
					t.Fatalf("failed branch changed: %+v %v", branch, err)
				}
				// A new turn may advance head. Same terminal retries still win
				// over missing/stale tokens, malformed payload and error metadata.
				if _, err := r.BeginTurn(ctx, runInput("next")); err != nil {
					t.Fatal(err)
				}
				before := lifecycleSnapshot(t, ctx, db)
				input.ExpectedHead = nil
				input.FinalMessage = &conversation.EntryMessage{}
				input.ErrorCode, input.ErrorMessage = "bad code!", "must be ignored"
				got, err := r.FinishAssistant(ctx, input)
				if err != nil || !reflect.DeepEqual(got, finished) {
					t.Fatalf("idempotent finish = %+v %v", got, err)
				}
				input.Status = conversation.MessageStatusCompleted
				if got, err := r.FinishAssistant(ctx, input); !errors.Is(err, domainerr.ErrConflict) || !reflect.DeepEqual(got, conversation.Message{}) {
					t.Fatalf("different terminal = %+v %v", got, err)
				}
				if err := r.AppendAssistantText(ctx, run.AssistantMessageID, "late"); !errors.Is(err, domainerr.ErrConflict) {
					t.Fatalf("late text = %v", err)
				}
				if got, err := r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{RunID: run.ID, ExpectedHead: lifecycleToken(t, run), Messages: []conversation.EntryMessage{lifecycleCall(t)}}); !errors.Is(err, domainerr.ErrConflict) || !reflect.DeepEqual(got, chat.CommitRunMessagesResult{}) {
					t.Fatalf("late facts = %+v %v", got, err)
				}
				if lifecycleSnapshot(t, ctx, db) != before {
					t.Fatal("terminal retry/late write mutated tables")
				}
			})
		}
	}
}

func TestAgentRunLifecycleRejectsInvalidFactsAndFinal(t *testing.T) {
	for _, kind := range []string{"pending facts", "running final via facts", "multiple calls", "user facts", "waiting call", "missing results", "duplicate results", "unknown call", "wrong tool", "wrong assistant", "result content", "unsupported policy", "raw result field", "forged preview", "capacity", "final absent", "final user", "final tools", "pending complete", "waiting complete", "failed partial final", "cancel partial final", "bad error code", "missing token"} {
		t.Run(kind, func(t *testing.T) {
			db, ctx, r := runFixture(t)
			turn := lifecycleBegin(t, ctx, r, kind != "pending facts" && kind != "pending complete")
			run := *turn.Run
			input := chat.CommitRunMessagesInput{RunID: run.ID, ExpectedHead: lifecycleToken(t, run), Messages: []conversation.EntryMessage{lifecycleCall(t)}}
			waiting := kind == "waiting call" || strings.Contains(kind, "results") || kind == "unknown call" || kind == "wrong tool" || kind == "wrong assistant" || kind == "result content" || kind == "raw result field" || kind == "forged preview" || kind == "waiting complete"
			if waiting {
				call := lifecycleCommitCall(t, ctx, r, run)
				run = call.Run
				input.ExpectedHead = entryTxToken(call.Head)
				input.Messages = lifecycleResults(t, call.Entries[0].ID)
			}
			finish := lifecycleFinish(t, run, conversation.MessageStatusCompleted)
			useFinish := false
			switch kind {
			case "running final via facts":
				input.Messages = []conversation.EntryMessage{entryTxMessage(conversation.EntryRoleAssistant)}
			case "multiple calls":
				input.Messages = append(input.Messages, lifecycleCall(t))
			case "user facts":
				input.Messages = []conversation.EntryMessage{entryTxMessage(conversation.EntryRoleUser)}
			case "waiting call":
				input.Messages = []conversation.EntryMessage{lifecycleCall(t)}
			case "missing results":
				input.Messages = input.Messages[:1]
			case "duplicate results":
				input.Messages[1] = input.Messages[0].Clone()
			case "unknown call":
				input.Messages[1].ToolResult.CallID = "unknown"
			case "wrong tool":
				input.Messages[1].ToolResult.ToolName = "other-tool"
			case "wrong assistant":
				input.Messages[1].ToolResult.CallEntryID = run.UserEntryID
			case "result content":
				input.Messages[1].Content = entryTxMessage(conversation.EntryRoleUser).Content
			case "unsupported policy":
				var call conversation.SafeToolCall
				if err := json.Unmarshal([]byte(`{"id":"c1","name":"lifecycle-search","policyVersion":"missing","fields":[]}`), &call); err != nil {
					t.Fatal(err)
				}
				input.Messages[0].Content[0].ToolCall = &call
			case "raw result field", "forged preview":
				raw := `{"policyVersion":"v1","fields":[{"name":"raw","value":"secret"}],"preview":"secret","truncated":false}`
				if kind == "forged preview" {
					raw = `{"policyVersion":"v1","fields":[{"name":"answer","value":"approved"}],"preview":"secret","truncated":false}`
				}
				if err := json.Unmarshal([]byte(raw), &input.Messages[1].ToolResult.Result); err != nil {
					t.Fatal(err)
				}
			case "capacity":
				r.tree.maxPayloadBytes = 1
			case "final absent":
				useFinish = true
				finish.FinalMessage = nil
			case "final user":
				useFinish = true
				final := entryTxMessage(conversation.EntryRoleUser)
				finish.FinalMessage = &final
			case "final tools":
				useFinish = true
				final := lifecycleCall(t)
				finish.FinalMessage = &final
			case "pending complete", "waiting complete":
				useFinish = true
			case "failed partial final":
				useFinish = true
				finish.Status, finish.ErrorCode = conversation.MessageStatusFailed, "model_failed"
			case "cancel partial final":
				useFinish = true
				finish.Status = conversation.MessageStatusCancelled
			case "bad error code":
				useFinish = true
				finish = lifecycleFinish(t, run, conversation.MessageStatusFailed)
				finish.ErrorCode = "raw error!"
			case "missing token":
				useFinish = true
				finish.ExpectedHead = nil
			}
			before := lifecycleSnapshot(t, ctx, db)
			if useFinish {
				if got, err := r.FinishAssistant(ctx, finish); err == nil || !reflect.DeepEqual(got, conversation.Message{}) {
					t.Fatalf("invalid finish = %+v %v", got, err)
				}
			} else {
				if got, err := r.CommitRunMessages(ctx, input); err == nil || !reflect.DeepEqual(got, chat.CommitRunMessagesResult{}) {
					t.Fatalf("invalid facts = %+v %v", got, err)
				}
			}
			if lifecycleSnapshot(t, ctx, db) != before {
				t.Fatal("invalid input changed database")
			}
		})
	}
}

func TestAgentRunLifecycleHeadTokenAndOwnership(t *testing.T) {
	for _, kind := range []string{"nil leaf", "stale version", "wrong leaf", "ABA", "database ahead", "missing head", "foreign last", "foreign assistant", "wrong closed", "corrupt payload", "partial group"} {
		for _, operation := range []string{"facts", "finish"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				db, ctx, r := runFixture(t)
				turn := lifecycleBegin(t, ctx, r, true)
				run := *turn.Run
				token := lifecycleToken(t, run)
				switch kind {
				case "nil leaf":
					token.ActiveLeafID = nil
				case "stale version":
					token.Version--
				case "wrong leaf":
					id := conversation.EntryID("unknown")
					token.ActiveLeafID = &id
				case "ABA":
					head, err := r.tree.MoveHead(ctx, conversation.MoveHeadInput{ConversationID: run.ConversationID, ExpectedHead: token})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := r.tree.MoveHead(ctx, conversation.MoveHeadInput{ConversationID: run.ConversationID, ExpectedHead: entryTxToken(head), TargetID: &run.LastEntryID}); err != nil {
						t.Fatal(err)
					}
				case "database ahead":
					if _, err := r.tree.Append(ctx, conversation.AppendEntryInput{ConversationID: run.ConversationID, ExpectedHead: token, Message: entryTxMessage(conversation.EntryRoleAssistant)}); err != nil {
						t.Fatal(err)
					}
				case "missing head":
					if _, err := db.ExecContext(ctx, `DELETE FROM conversation_entry_heads`); err != nil {
						t.Fatal(err)
					}
				case "foreign last", "foreign assistant":
					now := time.Now().UTC()
					if err := NewConversationRepository(db).Create(ctx, conversation.Conversation{ID: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
						t.Fatal(err)
					}
					var other chat.BeginTurnResult
					var err error
					if kind == "foreign last" {
						input := runInput("other")
						input.ConversationID = "other"
						other, err = r.BeginTurn(ctx, input)
						if err != nil {
							t.Fatal(err)
						}
					}
					// Disable FK only to simulate existing corrupt ownership, then
					// reenable it before the operation under test.
					if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
						t.Fatal(err)
					}
					if kind == "foreign last" {
						_, err = db.ExecContext(ctx, `UPDATE agent_runs SET user_entry_id = ?, last_entry_id = ?, last_closed_entry_id = ? WHERE id = ?`, other.Run.UserEntryID, other.Run.UserEntryID, other.Run.UserEntryID, run.ID)
						token.ActiveLeafID = &other.Run.UserEntryID
					} else {
						_, err = db.ExecContext(ctx, `UPDATE messages SET conversation_id = ? WHERE id = ?`, "other", run.AssistantMessageID)
					}
					if err != nil {
						t.Fatal(err)
					}
					if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
						t.Fatal(err)
					}
				case "wrong closed":
					call := lifecycleCommitCall(t, ctx, r, run)
					run, token = call.Run, entryTxToken(call.Head)
					if _, err := db.ExecContext(ctx, `UPDATE agent_runs SET last_closed_entry_id = last_entry_id WHERE id = ?`, run.ID); err != nil {
						t.Fatal(err)
					}
				case "corrupt payload":
					if _, err := db.ExecContext(ctx, `DROP TRIGGER entry_nodes_no_update`); err != nil {
						t.Fatal(err)
					}
					if _, err := db.ExecContext(ctx, `UPDATE conversation_entry_nodes SET payload_json = '{}'`); err != nil {
						t.Fatal(err)
					}
				case "partial group":
					call := lifecycleCommitCall(t, ctx, r, run)
					run = call.Run
					results := lifecycleResults(t, call.Entries[0].ID)
					partial, err := r.tree.Append(ctx, conversation.AppendEntryInput{ConversationID: run.ConversationID, ExpectedHead: entryTxToken(call.Head), Message: results[1]})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := db.ExecContext(ctx, `UPDATE agent_runs SET last_entry_id = ?, head_version = ? WHERE id = ?`, partial.Entry.ID, partial.Head.Version, run.ID); err != nil {
						t.Fatal(err)
					}
					run.LastEntryID, run.HeadVersion = partial.Entry.ID, partial.Head.Version
					token = entryTxToken(partial.Head)
				}
				before := lifecycleSnapshot(t, ctx, db)
				if operation == "facts" {
					if got, err := r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{RunID: run.ID, ExpectedHead: token, Messages: []conversation.EntryMessage{lifecycleCall(t)}}); err == nil || !reflect.DeepEqual(got, chat.CommitRunMessagesResult{}) {
						t.Fatalf("unsafe facts = %+v %v", got, err)
					}
				} else {
					input := lifecycleFinish(t, run, conversation.MessageStatusFailed)
					input.ExpectedHead = &token
					if got, err := r.FinishAssistant(ctx, input); err == nil || !reflect.DeepEqual(got, conversation.Message{}) {
						t.Fatalf("unsafe finish = %+v %v", got, err)
					}
				}
				if lifecycleSnapshot(t, ctx, db) != before {
					t.Fatal("conflict/corruption mutated database")
				}
			})
		}
	}
}

func TestAgentRunLifecycleCommitFailuresReturnZeroAndRollback(t *testing.T) {
	for _, operation := range []string{"start", "call", "results", "complete", "rewind", "closed failure", "text"} {
		t.Run(operation, func(t *testing.T) {
			db, ctx, r := runFixture(t)
			turn := lifecycleBegin(t, ctx, r, operation != "start")
			run := *turn.Run
			input := chat.CommitRunMessagesInput{RunID: run.ID, ExpectedHead: lifecycleToken(t, run), Messages: []conversation.EntryMessage{lifecycleCall(t)}}
			if operation == "results" || operation == "rewind" || operation == "closed failure" {
				call := lifecycleCommitCall(t, ctx, r, run)
				run = call.Run
				input.ExpectedHead, input.Messages = entryTxToken(call.Head), lifecycleResults(t, call.Entries[0].ID)
				if operation == "closed failure" {
					result, err := r.CommitRunMessages(ctx, input)
					if err != nil {
						t.Fatal(err)
					}
					run = result.Run
				}
			}
			before := lifecycleSnapshot(t, ctx, db)
			trigger := `CREATE TRIGGER lifecycle_commit AFTER UPDATE ON agent_runs BEGIN UPDATE agent_runs SET base_entry_id = 'missing' WHERE id = NEW.id; END`
			if operation == "text" {
				trigger = `CREATE TRIGGER lifecycle_commit AFTER UPDATE ON messages BEGIN UPDATE agent_runs SET base_entry_id = 'missing' WHERE assistant_message_id = NEW.id; END`
			}
			if _, err := db.ExecContext(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			// Show core success and provisional values before the deferred FK
			// fails COMMIT, then independently exercise the public boundary.
			if operation == "call" || operation == "results" || operation == "complete" || operation == "rewind" || operation == "closed failure" {
				callbackSucceeded := false
				err := db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
					core := *r
					core.db = nil
					if operation == "call" || operation == "results" {
						got, err := core.commitRunMessagesTx(ctx, tx, input)
						if err != nil {
							return err
						}
						if len(got.Entries) == 0 {
							t.Fatal("no provisional facts")
						}
					} else {
						status := conversation.MessageStatusFailed
						if operation == "complete" {
							status = conversation.MessageStatusCompleted
						}
						got, err := core.finishRunAssistantTx(ctx, tx, lifecycleFinish(t, run, status))
						if err != nil {
							return err
						}
						if got.Status != status {
							t.Fatal("no provisional terminal")
						}
					}
					callbackSucceeded = true
					return nil
				})
				if !callbackSucceeded || err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
					t.Fatalf("not deferred COMMIT: callback=%t error=%v", callbackSucceeded, err)
				}
			}
			var err error
			switch operation {
			case "start":
				var got agentrun.Run
				got, err = r.MarkRunning(ctx, run.ID)
				if !reflect.DeepEqual(got, agentrun.Run{}) {
					t.Fatalf("leaked Run: %+v", got)
				}
			case "call", "results":
				var got chat.CommitRunMessagesResult
				got, err = r.CommitRunMessages(ctx, input)
				if !reflect.DeepEqual(got, chat.CommitRunMessagesResult{}) {
					t.Fatalf("leaked facts: %+v", got)
				}
			case "text":
				err = r.AppendAssistantText(ctx, run.AssistantMessageID, "must roll back")
			default:
				status := conversation.MessageStatusFailed
				if operation == "complete" {
					status = conversation.MessageStatusCompleted
				}
				var got conversation.Message
				got, err = r.FinishAssistant(ctx, lifecycleFinish(t, run, status))
				if !reflect.DeepEqual(got, conversation.Message{}) {
					t.Fatalf("leaked terminal: %+v", got)
				}
			}
			if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
				t.Fatalf("expected deferred COMMIT failure: %v", err)
			}
			if lifecycleSnapshot(t, ctx, db) != before {
				t.Fatal("COMMIT failure left partial state")
			}
			if _, err := db.ExecContext(ctx, `DROP TRIGGER lifecycle_commit`); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAgentRunLifecycleMidTransactionFailureRollsBack(t *testing.T) {
	for _, operation := range []string{"results second entry", "results run CAS", "final message", "final touch", "rewind message", "rewind head CAS"} {
		t.Run(operation, func(t *testing.T) {
			db, ctx, r := runFixture(t)
			turn := lifecycleBegin(t, ctx, r, true)
			run := *turn.Run
			var call chat.CommitRunMessagesResult
			if strings.HasPrefix(operation, "results") || strings.HasPrefix(operation, "rewind") {
				call = lifecycleCommitCall(t, ctx, r, run)
				run = call.Run
			}
			before := lifecycleSnapshot(t, ctx, db)
			triggers := map[string]string{
				"results second entry": `CREATE TRIGGER lifecycle_fail BEFORE INSERT ON conversation_entry_nodes WHEN NEW.depth = 3 BEGIN SELECT RAISE(ABORT, 'second result failed'); END`,
				"results run CAS":      `CREATE TRIGGER lifecycle_fail BEFORE UPDATE ON agent_runs BEGIN SELECT RAISE(IGNORE); END`,
				"final message":        `CREATE TRIGGER lifecycle_fail BEFORE UPDATE ON messages BEGIN SELECT RAISE(ABORT, 'message failed'); END`,
				"final touch":          `CREATE TRIGGER lifecycle_fail BEFORE UPDATE ON conversations BEGIN SELECT RAISE(ABORT, 'touch failed'); END`,
				"rewind message":       `CREATE TRIGGER lifecycle_fail BEFORE UPDATE ON messages BEGIN SELECT RAISE(ABORT, 'message failed'); END`,
				"rewind head CAS":      `CREATE TRIGGER lifecycle_fail BEFORE UPDATE ON conversation_entry_heads BEGIN SELECT RAISE(IGNORE); END`,
			}
			if _, err := db.ExecContext(ctx, triggers[operation]); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(operation, "results") {
				if got, err := r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{RunID: run.ID, ExpectedHead: entryTxToken(call.Head), Messages: lifecycleResults(t, call.Entries[0].ID)}); err == nil || !reflect.DeepEqual(got, chat.CommitRunMessagesResult{}) {
					t.Fatalf("failed results = %+v %v", got, err)
				}
			} else {
				status := conversation.MessageStatusCompleted
				if strings.HasPrefix(operation, "rewind") {
					status = conversation.MessageStatusCancelled
				}
				if got, err := r.FinishAssistant(ctx, lifecycleFinish(t, run, status)); err == nil || !reflect.DeepEqual(got, conversation.Message{}) {
					t.Fatalf("failed finish = %+v %v", got, err)
				}
			}
			if lifecycleSnapshot(t, ctx, db) != before {
				t.Fatal("later failure left partial transaction")
			}
		})
	}
}

func TestAgentRunLifecycleLegacyAndLookup(t *testing.T) {
	db, ctx, r := runFixture(t)
	turn, err := r.turns.BeginTurn(ctx, runInput("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	before := lifecycleSnapshot(t, ctx, db)
	if _, err := r.FinishAssistant(ctx, chat.FinishAssistantInput{AssistantMessageID: turn.AssistantMessage.ID, Status: conversation.MessageStatusFailed, ErrorCode: "model_failed"}); !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("legacy streaming finish = %v", err)
	}
	if err := r.AppendAssistantText(ctx, turn.AssistantMessage.ID, "unowned"); !errors.Is(err, domainerr.ErrNotFound) {
		t.Fatalf("legacy text = %v", err)
	}
	if lifecycleSnapshot(t, ctx, db) != before {
		t.Fatal("legacy streaming was modified")
	}
	finished, err := r.turns.FinishAssistant(ctx, chat.FinishAssistantInput{AssistantMessageID: turn.AssistantMessage.ID, Status: conversation.MessageStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	before = lifecycleSnapshot(t, ctx, db)
	got, err := r.FinishAssistant(ctx, chat.FinishAssistantInput{AssistantMessageID: turn.AssistantMessage.ID, Status: conversation.MessageStatusCompleted})
	if err != nil || !reflect.DeepEqual(got, finished) {
		t.Fatalf("legacy terminal = %+v %v", got, err)
	}
	if _, err := r.FinishAssistant(ctx, chat.FinishAssistantInput{AssistantMessageID: turn.AssistantMessage.ID, Status: conversation.MessageStatusCancelled}); !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("legacy different terminal = %v", err)
	}
	got, err = r.GetMessage(ctx, " "+finished.ID+" ")
	if err != nil || !reflect.DeepEqual(got, finished) {
		t.Fatalf("lookup = %+v %v", got, err)
	}
	for _, id := range []string{"", "missing"} {
		want := domainerr.ErrNotFound
		if id == "" {
			want = domainerr.ErrInvalidInput
		}
		if got, err := r.GetMessage(ctx, id); !errors.Is(err, want) || !reflect.DeepEqual(got, conversation.Message{}) {
			t.Fatalf("missing = %+v %v", got, err)
		}
	}
	if lifecycleSnapshot(t, ctx, db) != before {
		t.Fatal("legacy retry or lookup changed state")
	}
}

func TestAgentRunLifecycleConcurrentFactsAndUniqueTerminal(t *testing.T) {
	for _, operation := range []string{"start", "facts", "terminal"} {
		t.Run(operation, func(t *testing.T) {
			db, ctx, r := runFixture(t)
			turn := lifecycleBegin(t, ctx, r, operation != "start")
			run := *turn.Run
			input := chat.CommitRunMessagesInput{RunID: run.ID, ExpectedHead: lifecycleToken(t, run), Messages: []conversation.EntryMessage{lifecycleCall(t)}}
			final := lifecycleFinish(t, run, conversation.MessageStatusCompleted)
			cancel := lifecycleFinish(t, run, conversation.MessageStatusCancelled)
			var wg sync.WaitGroup
			errs := make([]error, 2)
			for i := range errs {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					switch operation {
					case "start":
						_, errs[i] = r.MarkRunning(ctx, run.ID)
					case "facts":
						_, errs[i] = r.CommitRunMessages(ctx, input)
					case "terminal":
						if i == 0 {
							_, errs[i] = r.FinishAssistant(ctx, final)
						} else {
							_, errs[i] = r.FinishAssistant(ctx, cancel)
						}
					}
				}(i)
			}
			wg.Wait()
			success := 0
			for _, err := range errs {
				if err == nil {
					success++
				} else if !errors.Is(err, domainerr.ErrConflict) {
					t.Fatal(err)
				}
			}
			if success != 1 {
				t.Fatalf("winners = %d, errors = %v", success, errs)
			}
			got, err := r.BeginTurn(ctx, runInput("lifecycle"))
			if err != nil {
				t.Fatal(err)
			}
			entries := 1
			if operation == "facts" || got.Run.Status == agentrun.StatusCompleted {
				entries++
			}
			assertRunCounts(t, ctx, db, 2, entries, 1, 1)
		})
	}
}
