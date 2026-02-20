package opencode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

func TestNewOpenCodeAgent(t *testing.T) {
	t.Parallel()
	ag := NewOpenCodeAgent()
	require.NotNil(t, ag)

	oc, ok := ag.(*OpenCodeAgent)
	require.True(t, ok)
	assert.NotNil(t, oc)
}

func TestOpenCodeAgent_Name(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	assert.Equal(t, agent.AgentNameOpenCode, ag.Name())
}

func TestOpenCodeAgent_Type(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	assert.Equal(t, agent.AgentTypeOpenCode, ag.Type())
}

func TestOpenCodeAgent_Description(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	assert.NotEmpty(t, ag.Description())
}

func TestOpenCodeAgent_ProtectedDirs(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	dirs := ag.ProtectedDirs()
	require.Len(t, dirs, 1)
	assert.Equal(t, ".opencode", dirs[0])
}

func TestOpenCodeAgent_GetHookConfigPath(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	assert.Equal(t, ".opencode/plugins/entire.ts", ag.GetHookConfigPath())
}

func TestOpenCodeAgent_SupportsHooks(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	assert.True(t, ag.SupportsHooks())
}

func TestOpenCodeAgent_GetSupportedHooks(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	hooks := ag.GetSupportedHooks()

	expected := []agent.HookType{
		agent.HookSessionStart,
		agent.HookSessionEnd,
		agent.HookUserPromptSubmit,
		agent.HookStop,
	}
	assert.Equal(t, expected, hooks)
}

func TestOpenCodeAgent_GetHookNames(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	names := ag.GetHookNames()

	expected := []string{
		HookNameSessionStart,
		HookNameSessionEnd,
		HookNameBeforeAgent,
		HookNameAfterAgent,
		HookNameAfterTool,
	}
	assert.Equal(t, expected, names)
}

func TestOpenCodeAgent_InterfaceCompliance(t *testing.T) {
	t.Parallel()

	t.Run("implements Agent", func(t *testing.T) {
		t.Parallel()
		var _ agent.Agent = (*OpenCodeAgent)(nil)
	})

	t.Run("implements HookHandler", func(t *testing.T) {
		t.Parallel()
		var _ agent.HookHandler = (*OpenCodeAgent)(nil)
	})

	t.Run("implements HookSupport", func(t *testing.T) {
		t.Parallel()
		var _ agent.HookSupport = (*OpenCodeAgent)(nil)
	})
}

func TestOpenCodeAgent_FormatResumeCommand(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	assert.Equal(t, "opencode", ag.FormatResumeCommand("any-session-id"))
}

func TestOpenCodeAgent_ResolveSessionFile(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	result := ag.ResolveSessionFile("/data/sessions", "sess-abc-123")
	assert.Equal(t, "/data/sessions/sess-abc-123.jsonl", result)
}

func TestOpenCodeAgent_GetSessionID(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	input := &agent.HookInput{SessionID: "test-session-xyz"}
	assert.Equal(t, "test-session-xyz", ag.GetSessionID(input))
}

func TestOpenCodeAgent_WriteSession_ReturnsNotSupported(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	err := ag.WriteSession(&agent.AgentSession{})
	assert.ErrorIs(t, err, agent.ErrResumeNotSupported)
}

func TestParseHookInput_SessionStart(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	input := `{"session_id":"sess-001"}`

	result, err := ag.ParseHookInput(agent.HookSessionStart, strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, "sess-001", result.SessionID)
	assert.Equal(t, agent.HookSessionStart, result.HookType)
}

func TestParseHookInput_SessionEnd(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	input := `{"session_id":"sess-002"}`

	result, err := ag.ParseHookInput(agent.HookSessionEnd, strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, "sess-002", result.SessionID)
	assert.Equal(t, agent.HookSessionEnd, result.HookType)
}

func TestParseHookInput_BeforeAgent(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	input := `{"session_id":"sess-003","prompt":"Fix the login bug"}`

	result, err := ag.ParseHookInput(agent.HookUserPromptSubmit, strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, "sess-003", result.SessionID)
	assert.Equal(t, "Fix the login bug", result.UserPrompt)
	assert.Equal(t, agent.HookUserPromptSubmit, result.HookType)
}

func TestParseHookInput_BeforeAgent_NoPrompt(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	input := `{"session_id":"sess-004"}`

	result, err := ag.ParseHookInput(agent.HookUserPromptSubmit, strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, "sess-004", result.SessionID)
	assert.Empty(t, result.UserPrompt)
}

func TestParseHookInput_AfterAgent_Enriched(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	input := `{
		"session_id": "sess-005",
		"modified_files": ["main.go", "utils.go"],
		"tokens": {"input": 1000, "output": 500, "cache_read": 200},
		"summary": "Refactored error handling"
	}`

	result, err := ag.ParseHookInput(agent.HookStop, strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, "sess-005", result.SessionID)
	assert.Equal(t, agent.HookStop, result.HookType)

	modFiles, ok := result.RawData["modified_files"].([]string)
	require.True(t, ok, "modified_files should be []string")
	assert.Equal(t, []string{"main.go", "utils.go"}, modFiles)

	tokens, ok := result.RawData["tokens"].(*WireToken)
	require.True(t, ok, "tokens should be *WireToken")
	assert.Equal(t, 1000, tokens.Input)
	assert.Equal(t, 500, tokens.Output)
	assert.Equal(t, 200, tokens.CacheRead)

	summary, ok := result.RawData["summary"].(string)
	require.True(t, ok)
	assert.Equal(t, "Refactored error handling", summary)
}

func TestParseHookInput_AfterAgent_Minimal(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	input := `{"session_id":"sess-006"}`

	result, err := ag.ParseHookInput(agent.HookStop, strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, "sess-006", result.SessionID)
	assert.Empty(t, result.RawData["modified_files"])
	assert.Nil(t, result.RawData["tokens"])
	assert.Empty(t, result.RawData["summary"])
}

func TestParseHookInput_AfterTool(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	input := `{
		"session_id": "sess-007",
		"tool_name": "edit",
		"tool_use_id": "call-abc",
		"tool_input": {"file_path": "main.go"}
	}`

	result, err := ag.ParseHookInput(agent.HookPreToolUse, strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, "sess-007", result.SessionID)
	assert.Equal(t, "call-abc", result.ToolUseID)
	assert.Equal(t, "edit", result.RawData["tool_name"])
}

func TestParseHookInput_Empty(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}

	_, err := ag.ParseHookInput(agent.HookSessionStart, strings.NewReader(""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty input")
}

func TestParseHookInput_InvalidJSON(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}

	_, err := ag.ParseHookInput(agent.HookSessionStart, strings.NewReader("not json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse")
}

func TestDetectPresence_WithOpenCodeDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	require.NoError(t, os.Mkdir(".opencode", 0o755))

	ag := &OpenCodeAgent{}
	present, err := ag.DetectPresence()
	require.NoError(t, err)
	assert.True(t, present)
}

func TestDetectPresence_WithOpenCodeJSON(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	require.NoError(t, os.WriteFile("opencode.json", []byte("{}"), 0o644))

	ag := &OpenCodeAgent{}
	present, err := ag.DetectPresence()
	require.NoError(t, err)
	assert.True(t, present)
}

func TestDetectPresence_NeitherExists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	ag := &OpenCodeAgent{}
	present, err := ag.DetectPresence()
	require.NoError(t, err)
	assert.False(t, present)
}

func TestInstallHooks(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	ag := &OpenCodeAgent{}
	count, err := ag.InstallHooks(false, false)
	require.NoError(t, err)
	assert.Equal(t, 5, count)

	pluginPath := filepath.Join(tmpDir, ".opencode", "plugins", "entire.ts")
	data, err := os.ReadFile(pluginPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "session-start")
	assert.Contains(t, string(data), "after-agent")
	assert.Contains(t, string(data), "entire")
}

func TestUninstallHooks(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	ag := &OpenCodeAgent{}
	_, err := ag.InstallHooks(false, false)
	require.NoError(t, err)

	require.NoError(t, ag.UninstallHooks())

	pluginPath := filepath.Join(tmpDir, ".opencode", "plugins", "entire.ts")
	_, err = os.Stat(pluginPath)
	assert.True(t, os.IsNotExist(err))
}

func TestAreHooksInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	ag := &OpenCodeAgent{}
	assert.False(t, ag.AreHooksInstalled())

	_, err := ag.InstallHooks(false, false)
	require.NoError(t, err)
	assert.True(t, ag.AreHooksInstalled())
}

func TestChunkTranscript_SmallContent(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	content := []byte(`{"role":"user","content":"hello"}` + "\n" + `{"role":"assistant","content":"hi"}`)

	chunks, err := ag.ChunkTranscript(content, agent.MaxChunkSize)
	require.NoError(t, err)
	assert.Len(t, chunks, 1)
}

func TestReassembleTranscript(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}

	chunk1 := []byte(`{"role":"user","content":"hello"}`)
	chunk2 := []byte(`{"role":"assistant","content":"hi"}`)

	result, err := ag.ReassembleTranscript([][]byte{chunk1, chunk2})
	require.NoError(t, err)
	assert.Contains(t, string(result), `"role":"user"`)
	assert.Contains(t, string(result), `"role":"assistant"`)
}

func TestReadTranscript(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "full.jsonl")
	expected := `{"role":"user","content":"test"}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(expected), 0o644))

	ag := &OpenCodeAgent{}
	data, err := ag.ReadTranscript(path)
	require.NoError(t, err)
	assert.Equal(t, expected, string(data))
}

func TestReadTranscript_NotFound(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	_, err := ag.ReadTranscript("/nonexistent/path")
	require.Error(t, err)
}

func TestReadMessagesFromStorage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OPENCODE_HOME", tmpDir)

	sessionID := "test-session-001"
	msgDir := filepath.Join(tmpDir, "storage", "message", sessionID)
	require.NoError(t, os.MkdirAll(msgDir, 0o755))

	msg1 := Message{ID: "msg-1", SessionID: sessionID, Role: "user", Time: MessageTime{Created: 100}}
	msg2 := Message{ID: "msg-2", SessionID: sessionID, Role: "assistant", Time: MessageTime{Created: 200}}

	writeJSON(t, filepath.Join(msgDir, "msg-1.json"), msg1)
	writeJSON(t, filepath.Join(msgDir, "msg-2.json"), msg2)

	ag := &OpenCodeAgent{}
	messages, err := ag.ReadMessagesFromStorage(sessionID)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Equal(t, "msg-1", messages[0].ID, "messages should be sorted by creation time")
	assert.Equal(t, "msg-2", messages[1].ID)
}

func TestReadMessagesFromStorage_NoDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OPENCODE_HOME", tmpDir)

	ag := &OpenCodeAgent{}
	messages, err := ag.ReadMessagesFromStorage("nonexistent-session")
	require.NoError(t, err)
	assert.Empty(t, messages)
}

func TestReadPartsForMessage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OPENCODE_HOME", tmpDir)

	msgID := "msg-abc"
	partDir := filepath.Join(tmpDir, "storage", "part", msgID)
	require.NoError(t, os.MkdirAll(partDir, 0o755))

	part1 := Part{ID: "p1", Type: "text", MsgID: msgID, Text: "Hello world"}
	part2 := Part{ID: "p2", Type: "tool", MsgID: msgID, Tool: "edit", State: &ToolState{
		Status: "completed",
		Input:  map[string]interface{}{"file_path": "main.go"},
	}}

	writeJSON(t, filepath.Join(partDir, "p1.json"), part1)
	writeJSON(t, filepath.Join(partDir, "p2.json"), part2)

	ag := &OpenCodeAgent{}
	parts, err := ag.ReadPartsForMessage(msgID)
	require.NoError(t, err)
	require.Len(t, parts, 2)
}

func TestGetMessageCount(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OPENCODE_HOME", tmpDir)

	sessionID := "test-session-count"
	msgDir := filepath.Join(tmpDir, "storage", "message", sessionID)
	require.NoError(t, os.MkdirAll(msgDir, 0o755))

	writeJSON(t, filepath.Join(msgDir, "msg-1.json"), Message{ID: "msg-1", Role: "user"})
	writeJSON(t, filepath.Join(msgDir, "msg-2.json"), Message{ID: "msg-2", Role: "assistant"})

	ag := &OpenCodeAgent{}
	assert.Equal(t, 2, ag.GetMessageCount(sessionID))
}

func TestGetMessageCount_EmptySession(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	assert.Equal(t, 0, ag.GetMessageCount(""))
	assert.Equal(t, 0, ag.GetMessageCount("nonexistent"))
}

func TestReadSession(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OPENCODE_HOME", tmpDir)

	sessionID := "test-read-session"
	msgDir := filepath.Join(tmpDir, "storage", "message", sessionID)
	require.NoError(t, os.MkdirAll(msgDir, 0o755))

	msg := Message{ID: "msg-1", SessionID: sessionID, Role: "assistant", Time: MessageTime{Created: 100}}
	writeJSON(t, filepath.Join(msgDir, "msg-1.json"), msg)

	partDir := filepath.Join(tmpDir, "storage", "part", "msg-1")
	require.NoError(t, os.MkdirAll(partDir, 0o755))
	part := Part{ID: "p1", Type: "tool", MsgID: "msg-1", Tool: "edit", State: &ToolState{
		Status: "completed",
		Input:  map[string]interface{}{"file_path": "app.go"},
	}}
	writeJSON(t, filepath.Join(partDir, "p1.json"), part)

	ag := &OpenCodeAgent{}
	session, err := ag.ReadSession(&agent.HookInput{SessionID: sessionID})
	require.NoError(t, err)
	assert.Equal(t, sessionID, session.SessionID)
	assert.Equal(t, agent.AgentNameOpenCode, session.AgentName)
	assert.Contains(t, session.ModifiedFiles, "app.go")
}

func TestReadSession_NoSessionID(t *testing.T) {
	t.Parallel()
	ag := &OpenCodeAgent{}
	_, err := ag.ReadSession(&agent.HookInput{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session ID is required")
}

func TestComputePerTurnTokenUsage_FirstTurn(t *testing.T) {
	t.Parallel()
	wire := &WireToken{Input: 1000, Output: 500, CacheRead: 200, CacheWrite: 100}

	usage := ComputePerTurnTokenUsage(wire, nil)
	require.NotNil(t, usage)
	assert.Equal(t, 1000, usage.InputTokens)
	assert.Equal(t, 500, usage.OutputTokens)
	assert.Equal(t, 200, usage.CacheReadTokens)
	assert.Equal(t, 100, usage.CacheCreationTokens)
	assert.Equal(t, 1, usage.APICallCount)
}

func TestComputePerTurnTokenUsage_WithBaseline(t *testing.T) {
	t.Parallel()
	wire := &WireToken{Input: 3000, Output: 1500, CacheRead: 600, CacheWrite: 300}
	baseline := &WireToken{Input: 1000, Output: 500, CacheRead: 200, CacheWrite: 100}

	usage := ComputePerTurnTokenUsage(wire, baseline)
	require.NotNil(t, usage)
	assert.Equal(t, 2000, usage.InputTokens)
	assert.Equal(t, 1000, usage.OutputTokens)
	assert.Equal(t, 400, usage.CacheReadTokens)
	assert.Equal(t, 200, usage.CacheCreationTokens)
}

func TestComputePerTurnTokenUsage_NilWire(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ComputePerTurnTokenUsage(nil, nil))
}

func TestComputePerTurnTokenUsage_NegativeDeltaClampsToZero(t *testing.T) {
	t.Parallel()
	wire := &WireToken{Input: 100, Output: 50}
	baseline := &WireToken{Input: 200, Output: 100}

	usage := ComputePerTurnTokenUsage(wire, baseline)
	require.NotNil(t, usage)
	assert.Equal(t, 0, usage.InputTokens, "negative deltas should clamp to 0")
	assert.Equal(t, 0, usage.OutputTokens)
}

func TestStoragePath_OpencodeHome(t *testing.T) {
	t.Setenv("OPENCODE_HOME", "/custom/opencode")
	t.Setenv("XDG_DATA_HOME", "")
	assert.Equal(t, "/custom/opencode/storage", StoragePath())
}

func TestStoragePath_XDGDataHome(t *testing.T) {
	t.Setenv("OPENCODE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "/custom/xdg")
	assert.Equal(t, "/custom/xdg/opencode/storage", StoragePath())
}

func TestIsFileModificationTool(t *testing.T) {
	t.Parallel()
	assert.True(t, isFileModificationTool("edit"))
	assert.True(t, isFileModificationTool("write"))
	assert.True(t, isFileModificationTool("multiedit"))
	assert.True(t, isFileModificationTool("patch"))
	assert.False(t, isFileModificationTool("read"))
	assert.False(t, isFileModificationTool("bash"))
	assert.False(t, isFileModificationTool(""))
}

func TestFindProjectID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OPENCODE_HOME", tmpDir)

	projectDir := filepath.Join(tmpDir, "storage", "project")
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	proj := Project{ID: "proj-abc", Directory: "/my/repo"}
	writeJSON(t, filepath.Join(projectDir, "proj-abc.json"), proj)

	id, err := FindProjectID("/my/repo")
	require.NoError(t, err)
	assert.Equal(t, "proj-abc", id)
}

func TestFindProjectID_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OPENCODE_HOME", tmpDir)

	projectDir := filepath.Join(tmpDir, "storage", "project")
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	id, err := FindProjectID("/nonexistent/repo")
	require.NoError(t, err)
	assert.Empty(t, id)
}

func TestOpenCodeAgent_Registered(t *testing.T) {
	t.Parallel()
	ag, err := agent.Get(agent.AgentNameOpenCode)
	require.NoError(t, err)
	assert.Equal(t, agent.AgentNameOpenCode, ag.Name())
}

func TestOpenCodeAgent_HookHandlerTypeAssertion(t *testing.T) {
	t.Parallel()
	ag, err := agent.Get(agent.AgentNameOpenCode)
	require.NoError(t, err)

	handler, ok := ag.(agent.HookHandler)
	require.True(t, ok, "OpenCodeAgent should implement HookHandler (Bug 1 fix: GetHookNames)")
	assert.Len(t, handler.GetHookNames(), 5)
}

func TestParseHookInput_PluginPayloadRoundtrip(t *testing.T) {
	t.Parallel()

	ag := &OpenCodeAgent{}

	payload := map[string]interface{}{
		"session_id":     "sess-rt",
		"modified_files": []string{"a.go", "b.go"},
		"tokens":         map[string]interface{}{"input": float64(500), "output": float64(250)},
		"cost":           0.01,
		"summary":        "Added feature X",
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := ag.ParseHookInput(agent.HookStop, bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "sess-rt", result.SessionID)

	mf, ok := result.RawData["modified_files"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"a.go", "b.go"}, mf)
}

func writeJSON(t *testing.T, path string, v interface{}) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
}
