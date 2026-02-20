// Package opencode implements the Agent interface for OpenCode.
package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

//nolint:gochecknoinits // Agent self-registration is the intended pattern
func init() {
	agent.Register(agent.AgentNameOpenCode, NewOpenCodeAgent)
}

// OpenCodeAgent implements the Agent interface for OpenCode.
//
//nolint:revive // OpenCodeAgent is clearer than Agent in this context
type OpenCodeAgent struct{}

// NewOpenCodeAgent creates a new OpenCode agent instance.
func NewOpenCodeAgent() agent.Agent {
	return &OpenCodeAgent{}
}

func (o *OpenCodeAgent) Name() agent.AgentName   { return agent.AgentNameOpenCode }
func (o *OpenCodeAgent) Type() agent.AgentType   { return agent.AgentTypeOpenCode }
func (o *OpenCodeAgent) Description() string     { return "OpenCode - open source AI coding agent" }
func (o *OpenCodeAgent) IsPreview() bool         { return true }
func (o *OpenCodeAgent) ProtectedDirs() []string { return []string{".opencode"} }

func (o *OpenCodeAgent) DetectPresence() (bool, error) {
	repoRoot, err := paths.RepoRoot()
	if err != nil {
		repoRoot = "."
	}

	// Check for opencode.json at repo root
	if _, err := os.Stat(filepath.Join(repoRoot, "opencode.json")); err == nil {
		return true, nil
	}
	// Check for .opencode/ directory
	if _, err := os.Stat(filepath.Join(repoRoot, ".opencode")); err == nil {
		return true, nil
	}
	return false, nil
}

// GetHookConfigPath returns the path to OpenCode's hook config file.
// OpenCode uses a plugin file instead of a JSON config.
func (o *OpenCodeAgent) GetHookConfigPath() string {
	return ".opencode/plugins/entire.ts"
}

// SupportsHooks returns true as OpenCode supports lifecycle hooks via plugins.
func (o *OpenCodeAgent) SupportsHooks() bool {
	return true
}

// GetSupportedHooks returns the hook types OpenCode supports.
func (o *OpenCodeAgent) GetSupportedHooks() []agent.HookType {
	return []agent.HookType{
		agent.HookSessionStart,
		agent.HookSessionEnd,
		agent.HookUserPromptSubmit,
		agent.HookStop,
	}
}

// ParseHookInput parses OpenCode hook input from stdin.
// OpenCode hooks provide session_id plus optional enriched metadata (modified_files, tokens, summary).
func (o *OpenCodeAgent) ParseHookInput(hookType agent.HookType, reader io.Reader) (*agent.HookInput, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	if len(data) == 0 {
		return nil, errors.New("empty input")
	}

	input := &agent.HookInput{
		HookType:  hookType,
		Timestamp: time.Now(),
		RawData:   make(map[string]interface{}),
	}

	switch hookType {
	case agent.HookSessionStart, agent.HookSessionEnd:
		var raw sessionHookRaw
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("failed to parse session hook: %w", err)
		}
		input.SessionID = raw.SessionID

	case agent.HookUserPromptSubmit:
		var raw beforeAgentHookRaw
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("failed to parse before-agent hook: %w", err)
		}
		input.SessionID = raw.SessionID
		input.UserPrompt = raw.Prompt

	case agent.HookStop:
		// after-agent hook: enriched payload with modified_files, tokens, summary
		var raw afterAgentHookRaw
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("failed to parse after-agent hook: %w", err)
		}
		input.SessionID = raw.SessionID
		if len(raw.ModifiedFiles) > 0 {
			input.RawData["modified_files"] = raw.ModifiedFiles
		}
		if raw.Tokens != nil {
			input.RawData["tokens"] = raw.Tokens
		}
		if raw.Summary != "" {
			input.RawData["summary"] = raw.Summary
		}

	case agent.HookPreToolUse, agent.HookPostToolUse:
		// OpenCode does not emit pre/post tool use hooks;
		// handled here only to satisfy exhaustive switch lint.
		var raw afterToolHookRaw
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("failed to parse tool hook: %w", err)
		}
		input.SessionID = raw.SessionID
		input.ToolUseID = raw.ToolUseID
		if raw.ToolName != "" {
			input.RawData["tool_name"] = raw.ToolName
		}
	}

	return input, nil
}

// ReadTranscript reads the synthesized JSONL transcript bytes for a session.
// OpenCode stores data in separate message/part files; the transcript is synthesized
// to a JSONL file during checkpoint creation.
func (o *OpenCodeAgent) ReadTranscript(sessionRef string) ([]byte, error) {
	data, err := os.ReadFile(sessionRef) //nolint:gosec // Path comes from agent hook input
	if err != nil {
		return nil, fmt.Errorf("failed to read transcript: %w", err)
	}
	return data, nil
}

// ChunkTranscript splits a JSONL transcript at line boundaries.
// OpenCode transcripts are synthesized as JSONL (same format as Claude Code).
func (o *OpenCodeAgent) ChunkTranscript(content []byte, maxSize int) ([][]byte, error) {
	chunks, err := agent.ChunkJSONL(content, maxSize)
	if err != nil {
		return nil, fmt.Errorf("failed to chunk JSONL transcript: %w", err)
	}
	return chunks, nil
}

// ReassembleTranscript concatenates JSONL chunks with newlines.
func (o *OpenCodeAgent) ReassembleTranscript(chunks [][]byte) ([]byte, error) {
	return agent.ReassembleJSONL(chunks), nil
}

func (o *OpenCodeAgent) GetSessionID(input *agent.HookInput) string {
	return input.SessionID
}

// GetSessionDir returns the OpenCode storage directory (XDG data path).
func (o *OpenCodeAgent) GetSessionDir(_ string) (string, error) {
	return StoragePath(), nil
}

func (o *OpenCodeAgent) ResolveSessionFile(sessionDir, agentSessionID string) string {
	return filepath.Join(sessionDir, agentSessionID+".jsonl")
}

func (o *OpenCodeAgent) ReadSession(input *agent.HookInput) (*agent.AgentSession, error) {
	if input.SessionID == "" {
		return nil, errors.New("session ID is required")
	}

	messages, err := o.ReadMessagesFromStorage(input.SessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to read messages: %w", err)
	}

	var modifiedFiles []string
	for _, msg := range messages {
		if msg.Role != roleAssistant {
			continue
		}
		parts, partsErr := o.ReadPartsForMessage(msg.ID)
		if partsErr != nil {
			continue
		}
		for _, part := range parts {
			if part.Type != partTypeTool || part.State == nil {
				continue
			}
			if isFileModificationTool(part.Tool) {
				if fp, ok := part.State.Input["file_path"].(string); ok && fp != "" {
					modifiedFiles = append(modifiedFiles, fp)
				}
			}
		}
	}

	// Synthesize NativeData as a JSON summary
	nativeData, marshalErr := json.Marshal(map[string]interface{}{
		"session_id":     input.SessionID,
		"message_count":  len(messages),
		"modified_files": modifiedFiles,
	})
	if marshalErr != nil {
		return nil, fmt.Errorf("failed to marshal native data: %w", marshalErr)
	}

	return &agent.AgentSession{
		SessionID:     input.SessionID,
		AgentName:     o.Name(),
		StartTime:     time.Now(),
		NativeData:    nativeData,
		ModifiedFiles: modifiedFiles,
	}, nil
}

func (o *OpenCodeAgent) WriteSession(_ *agent.AgentSession) error {
	return agent.ErrResumeNotSupported
}

func (o *OpenCodeAgent) FormatResumeCommand(_ string) string {
	return "opencode"
}

// ReadMessagesFromStorage reads all messages for a session, sorted by creation time.
func (o *OpenCodeAgent) ReadMessagesFromStorage(sessionID string) ([]Message, error) {
	msgDir := filepath.Join(StoragePath(), "message", sessionID)
	entries, err := os.ReadDir(msgDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read message dir: %w", err)
	}

	var messages []Message
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(msgDir, entry.Name())) //nolint:gosec // path from controlled OpenCode storage directory
		if readErr != nil {
			continue
		}
		var msg Message
		if jsonErr := json.Unmarshal(data, &msg); jsonErr != nil {
			continue
		}
		messages = append(messages, msg)
	}

	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Time.Created < messages[j].Time.Created
	})
	return messages, nil
}

// ReadPartsForMessage reads all parts for a message, sorted alphabetically.
func (o *OpenCodeAgent) ReadPartsForMessage(messageID string) ([]Part, error) {
	partDir := filepath.Join(StoragePath(), "part", messageID)
	entries, err := os.ReadDir(partDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read part dir: %w", err)
	}

	var parts []Part
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(partDir, entry.Name())) //nolint:gosec // path from controlled OpenCode storage directory
		if readErr != nil {
			continue
		}
		var part Part
		if jsonErr := json.Unmarshal(data, &part); jsonErr != nil {
			continue
		}
		parts = append(parts, part)
	}
	return parts, nil
}

// GetMessageCount counts message files in OpenCode's storage for a session.
func (o *OpenCodeAgent) GetMessageCount(sessionID string) int {
	if sessionID == "" {
		return 0
	}
	msgDir := filepath.Join(StoragePath(), "message", sessionID)
	entries, err := os.ReadDir(msgDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	return count
}

func isFileModificationTool(toolName string) bool {
	for _, name := range FileModificationTools {
		if toolName == name {
			return true
		}
	}
	return false
}
