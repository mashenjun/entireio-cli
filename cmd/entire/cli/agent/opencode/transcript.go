package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SynthesizeTranscript reads OpenCode messages and parts from storage and writes
// a full.jsonl file matching the transcript.Line format:
//
//	{"type":"user","uuid":"msg_xxx","message":{"content":"prompt text"}}
//	{"type":"assistant","uuid":"msg_yyy","message":{"content":[...]}}
func SynthesizeTranscript(transcriptPath, sessionID string, ag *OpenCodeAgent) error {
	messages, err := ag.ReadMessagesFromStorage(sessionID)
	if err != nil {
		return fmt.Errorf("failed to read messages: %w", err)
	}

	var lines []string
	for _, msg := range messages {
		parts, partsErr := ag.ReadPartsForMessage(msg.ID)
		if partsErr != nil {
			continue
		}

		entryType := roleAssistant
		if msg.Role == roleUser {
			entryType = roleUser
		}

		content := buildMessageContent(msg.Role, parts)

		entry := map[string]interface{}{
			"type": entryType,
			"uuid": msg.ID,
			"message": map[string]interface{}{
				"content": content,
			},
		}

		lineBytes, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			continue
		}
		lines = append(lines, string(lineBytes))
	}

	if err := os.WriteFile(transcriptPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return fmt.Errorf("failed to write transcript: %w", err)
	}
	return nil
}

// buildMessageContent constructs the content field for a transcript line.
// User messages: plain string. Assistant messages: array of content blocks.
func buildMessageContent(role string, parts []Part) interface{} {
	if role == roleUser {
		var sb strings.Builder
		for _, part := range parts {
			if part.Type == partTypeText && part.Text != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(part.Text)
			}
		}
		return sb.String()
	}

	// Assistant: array of content blocks
	var blocks []map[string]interface{}
	for _, part := range parts {
		switch part.Type {
		case partTypeText:
			if part.Text != "" {
				blocks = append(blocks, map[string]interface{}{
					"type": partTypeText,
					"text": part.Text,
				})
			}
		case partTypeTool:
			block := map[string]interface{}{
				"type": "tool_use",
				"name": part.Tool,
			}
			if part.CallID != "" {
				block["id"] = part.CallID
			}
			if part.State != nil && part.State.Input != nil {
				block["input"] = part.State.Input
			}
			blocks = append(blocks, block)
		}
	}
	if len(blocks) == 0 {
		blocks = []map[string]interface{}{}
	}
	return blocks
}

// ExtractSessionMetadata reads OpenCode storage directly to gather metadata
// when the enriched plugin payload is unavailable (fallback path).
type SessionMetadata struct {
	ModifiedFiles []string
	Tokens        *WireToken
	Summary       string
}

// ExtractSessionMetadata reads messages from the given offset and extracts metadata.
func ExtractSessionMetadata(sessionID string, startOffset int, ag *OpenCodeAgent) *SessionMetadata {
	messages, err := ag.ReadMessagesFromStorage(sessionID)
	if err != nil {
		return &SessionMetadata{}
	}

	meta := &SessionMetadata{}

	// Sum tokens across all messages
	var totalInput, totalOutput, totalCacheRead, totalCacheWrite int
	for _, msg := range messages {
		totalInput += msg.Tokens.Input
		totalOutput += msg.Tokens.Output
		totalCacheRead += msg.Tokens.CacheRead
		totalCacheWrite += msg.Tokens.CacheWrite
	}
	if totalInput > 0 || totalOutput > 0 {
		meta.Tokens = &WireToken{
			Input:      totalInput,
			Output:     totalOutput,
			CacheRead:  totalCacheRead,
			CacheWrite: totalCacheWrite,
		}
	}

	// Extract modified files from messages since startOffset
	fileSet := make(map[string]bool)
	for i := startOffset; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role != roleAssistant {
			continue
		}
		parts, partsErr := ag.ReadPartsForMessage(msg.ID)
		if partsErr != nil {
			continue
		}
		for _, part := range parts {
			if part.Type == partTypeTool && part.State != nil && isFileModificationTool(part.Tool) {
				if fp, ok := part.State.Input["file_path"].(string); ok && fp != "" {
					if !fileSet[fp] {
						fileSet[fp] = true
						meta.ModifiedFiles = append(meta.ModifiedFiles, fp)
					}
				}
			}
		}
	}

	// Extract summary from last assistant message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == roleAssistant {
			parts, partsErr := ag.ReadPartsForMessage(messages[i].ID)
			if partsErr != nil {
				break
			}
			for _, part := range parts {
				if part.Type == partTypeText && part.Text != "" {
					meta.Summary = part.Text
					break
				}
			}
			break
		}
	}

	return meta
}
