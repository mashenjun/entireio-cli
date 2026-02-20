package opencode

import (
	"encoding/json"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// sessionHookRaw is the JSON from session-start / session-end hooks.
type sessionHookRaw struct {
	SessionID string `json:"session_id"`
}

// beforeAgentHookRaw is the JSON from before-agent hooks.
type beforeAgentHookRaw struct {
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt,omitempty"`
}

// afterAgentHookRaw is the enriched JSON from after-agent hooks.
// The plugin collects metadata from OpenCode's storage before invoking the hook.
type afterAgentHookRaw struct {
	SessionID     string     `json:"session_id"`
	ModifiedFiles []string   `json:"modified_files,omitempty"`
	Tokens        *WireToken `json:"tokens,omitempty"`
	Cost          float64    `json:"cost,omitempty"`
	Summary       string     `json:"summary,omitempty"`
}

// afterToolHookRaw is the JSON from after-tool hooks.
type afterToolHookRaw struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
}

// WireToken is the token usage format from the enriched after-agent plugin payload.
// These are SESSION-CUMULATIVE values (plugin sums all messages).
// Must be converted to per-turn deltas via ComputePerTurnTokenUsage before passing to SaveContext.
type WireToken struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cache_read,omitempty"`
	CacheWrite int `json:"cache_write,omitempty"`
	Reasoning  int `json:"reasoning,omitempty"`
}

// ComputePerTurnTokenUsage computes per-checkpoint token deltas from
// session-cumulative wire totals. prevBaseline is the accumulated total from
// SessionState.TokenUsage converted back to wire format.
// On the first turn, prevBaseline is nil so delta = wire totals.
func ComputePerTurnTokenUsage(wire *WireToken, prevBaseline *WireToken) *agent.TokenUsage {
	if wire == nil {
		return nil
	}
	deltaInput := wire.Input
	deltaOutput := wire.Output
	deltaCacheRead := wire.CacheRead
	deltaCacheWrite := wire.CacheWrite
	if prevBaseline != nil {
		deltaInput -= prevBaseline.Input
		deltaOutput -= prevBaseline.Output
		deltaCacheRead -= prevBaseline.CacheRead
		deltaCacheWrite -= prevBaseline.CacheWrite
	}
	return &agent.TokenUsage{
		InputTokens:         max(0, deltaInput),
		OutputTokens:        max(0, deltaOutput),
		CacheReadTokens:     max(0, deltaCacheRead),
		CacheCreationTokens: max(0, deltaCacheWrite),
		APICallCount:        1,
	}
}

// Message represents an OpenCode message from storage/message/<sesID>/<msgID>.json.
type Message struct {
	ID        string       `json:"id"`
	SessionID string       `json:"session_id"`
	Role      string       `json:"role"`
	Model     MessageModel `json:"model"`
	Tokens    MessageToken `json:"tokens"`
	Cost      float64      `json:"cost"`
	Time      MessageTime  `json:"time"`
}

// MessageModel holds the model identifiers.
type MessageModel struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

// MessageToken represents per-message token usage from OpenCode storage.
type MessageToken struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Reasoning  int `json:"reasoning"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

// MessageTime holds message timestamps (millisecond epoch).
type MessageTime struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
}

// Part represents an OpenCode part from storage/part/<msgID>/<partID>.json.
// Discriminated union on Type.
type Part struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	MsgID string `json:"messageID"`

	// text part
	Text string `json:"text,omitempty"`

	// tool part
	CallID string     `json:"callID,omitempty"`
	Tool   string     `json:"tool,omitempty"`
	State  *ToolState `json:"state,omitempty"`
}

// ToolState holds the status and I/O of a tool invocation.
type ToolState struct {
	Status string                 `json:"status"`
	Input  map[string]interface{} `json:"input,omitempty"`
	Output string                 `json:"output,omitempty"`
	Title  string                 `json:"title,omitempty"`
}

// Project represents an OpenCode project from storage/project/<projID>.json.
type Project struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
}

// Message role and part type constants used across OpenCode storage parsing.
const (
	roleUser      = "user"
	roleAssistant = "assistant"
	partTypeText  = "text"
	partTypeTool  = "tool"
)

// FileModificationTools lists OpenCode tool names that modify files.
var FileModificationTools = []string{
	"edit",
	"write",
	"multiedit",
	"patch",
}
