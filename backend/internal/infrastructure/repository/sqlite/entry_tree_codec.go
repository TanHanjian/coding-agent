package sqlite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"interview-memory-agent/backend/internal/domain/conversation"
)

func EncodeEntryMessage(msg conversation.EntryMessage) ([]byte, error) {
	if err := conversation.ValidateEntryMessage(msg); err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

func DecodeEntryMessage(payloadVersion int, data []byte) (conversation.EntryMessage, error) {
	if payloadVersion != conversation.EntryPayloadV1 {
		return conversation.EntryMessage{}, fmt.Errorf("%w: version %d is not supported", conversation.ErrEntryUnsupportedVersion, payloadVersion)
	}

	if err := checkDuplicateJSONKeys(data); err != nil {
		return conversation.EntryMessage{}, err
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var msg conversation.EntryMessage
	if err := dec.Decode(&msg); err != nil {
		return conversation.EntryMessage{}, fmt.Errorf("%w: decode entry message failed: %v", conversation.ErrEntryCorrupt, err)
	}
	if dec.More() {
		return conversation.EntryMessage{}, fmt.Errorf("%w: trailing data after JSON", conversation.ErrEntryCorrupt)
	}

	if err := conversation.ValidateEntryMessage(msg); err != nil {
		return conversation.EntryMessage{}, fmt.Errorf("%w: decoded message validation failed: %v", conversation.ErrEntryCorrupt, err)
	}

	return msg, nil
}

func checkDuplicateJSONKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	type frame struct {
		isObject  bool
		expectKey bool
		seenKeys  map[string]bool
	}
	var stack []frame

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: invalid json syntax: %v", conversation.ErrEntryCorrupt, err)
		}

		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{':
				if len(stack) > 0 && stack[len(stack)-1].isObject && !stack[len(stack)-1].expectKey {
					stack[len(stack)-1].expectKey = true
				}
				stack = append(stack, frame{isObject: true, expectKey: true, seenKeys: make(map[string]bool)})
			case '}':
				if len(stack) == 0 || !stack[len(stack)-1].isObject {
					return fmt.Errorf("%w: mismatched closing brace", conversation.ErrEntryCorrupt)
				}
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && stack[len(stack)-1].isObject && !stack[len(stack)-1].expectKey {
					stack[len(stack)-1].expectKey = true
				}
			case '[':
				if len(stack) > 0 && stack[len(stack)-1].isObject && !stack[len(stack)-1].expectKey {
					stack[len(stack)-1].expectKey = true
				}
				stack = append(stack, frame{isObject: false})
			case ']':
				if len(stack) == 0 || stack[len(stack)-1].isObject {
					return fmt.Errorf("%w: mismatched closing bracket", conversation.ErrEntryCorrupt)
				}
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && stack[len(stack)-1].isObject && !stack[len(stack)-1].expectKey {
					stack[len(stack)-1].expectKey = true
				}
			}
		} else {
			if len(stack) > 0 && stack[len(stack)-1].isObject {
				top := &stack[len(stack)-1]
				if top.expectKey {
					key, ok := tok.(string)
					if !ok {
						return fmt.Errorf("%w: expected string key in object", conversation.ErrEntryCorrupt)
					}
					if top.seenKeys[key] {
						return fmt.Errorf("%w: duplicate JSON key %q", conversation.ErrEntryCorrupt, key)
					}
					top.seenKeys[key] = true
					top.expectKey = false
				} else {
					top.expectKey = true
				}
			}
		}
	}
	return nil
}
