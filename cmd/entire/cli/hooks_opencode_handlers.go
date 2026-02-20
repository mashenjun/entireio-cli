// hooks_opencode_handlers.go contains OpenCode-specific hook handler implementations.
// These are called by the hook registry in hook_registry.go.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/opencode"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
)

type openCodeEnrichedData struct {
	modifiedFiles []string
	wireTokens    *opencode.WireToken
	summary       string
	userPrompt    string
}

func handleOpenCodeBeforeAgent() error {
	ag, err := agent.Get(agent.AgentNameOpenCode)
	if err != nil {
		return fmt.Errorf("failed to get opencode agent: %w", err)
	}

	input, err := ag.ParseHookInput(agent.HookUserPromptSubmit, os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to parse hook input: %w", err)
	}

	logCtx := logging.WithAgent(logging.WithComponent(context.Background(), "hooks"), ag.Name())
	logging.Info(logCtx, "opencode-before-agent",
		slog.String("hook", "before-agent"),
		slog.String("hook_type", "agent"),
		slog.String("model_session_id", input.SessionID),
	)

	if input.SessionID == "" {
		return errors.New("no session_id in input")
	}

	if err := CaptureOpenCodePrePromptState(input.SessionID); err != nil {
		return fmt.Errorf("failed to capture pre-prompt state: %w", err)
	}

	strat := GetStrategy()

	if err := strat.EnsureSetup(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to ensure strategy setup: %v\n", err)
	}

	if initializer, ok := strat.(strategy.SessionInitializer); ok {
		agentType := ag.Type()
		// OpenCode has no live transcript file — pass empty string.
		// The synthesized transcript is written at after-agent time.
		if err := initializer.InitializeSession(input.SessionID, agentType, "", input.UserPrompt); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to initialize session state: %v\n", err)
		}
	}

	return nil
}

func handleOpenCodeAfterAgent() error { //nolint:cyclop // mirrors Gemini's handleGeminiAfterAgent structure
	ag, err := agent.Get(agent.AgentNameOpenCode)
	if err != nil {
		return fmt.Errorf("failed to get opencode agent: %w", err)
	}

	input, err := ag.ParseHookInput(agent.HookStop, os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to parse hook input: %w", err)
	}

	logCtx := logging.WithAgent(logging.WithComponent(context.Background(), "hooks"), ag.Name())
	logging.Info(logCtx, "opencode-after-agent",
		slog.String("hook", "after-agent"),
		slog.String("hook_type", "agent"),
		slog.String("model_session_id", input.SessionID),
	)

	sessionID := input.SessionID
	if sessionID == "" {
		sessionID = unknownSessionID
	}

	if repo, repoErr := strategy.OpenRepository(); repoErr == nil && strategy.IsEmptyRepository(repo) {
		fmt.Fprintln(os.Stderr, "Entire: skipping checkpoint. Will activate after first commit.")
		return NewSilentError(strategy.ErrEmptyRepository)
	}

	preState, err := LoadPrePromptState(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load pre-prompt state: %v\n", err)
	}

	enriched := extractOpenCodeEnrichedData(input, ag, sessionID, preState)

	sessionDir := paths.SessionMetadataDirFromSessionID(sessionID)
	sessionDirAbs, err := paths.AbsPath(sessionDir)
	if err != nil {
		sessionDirAbs = sessionDir
	}

	if err := writeOpenCodeSessionMetadata(sessionDirAbs, sessionID, enriched); err != nil {
		return fmt.Errorf("failed to write session metadata: %w", err)
	}

	transcriptPath := filepath.Join(sessionDirAbs, paths.TranscriptFileName)

	repoRoot, err := paths.RepoRoot()
	if err != nil {
		return fmt.Errorf("failed to get repo root: %w", err)
	}

	changes, err := DetectFileChanges(preState.PreUntrackedFiles())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to compute file changes: %v\n", err)
	}

	relModifiedFiles := FilterAndNormalizePaths(enriched.modifiedFiles, repoRoot)
	var relNewFiles, relDeletedFiles []string
	if changes != nil {
		relNewFiles = FilterAndNormalizePaths(changes.New, repoRoot)
		relDeletedFiles = FilterAndNormalizePaths(changes.Deleted, repoRoot)
	}

	totalChanges := len(relModifiedFiles) + len(relNewFiles) + len(relDeletedFiles)
	if totalChanges == 0 {
		fmt.Fprintln(os.Stderr, "No files were modified during this session")
		transitionSessionTurnEnd(sessionID)
		if cleanupErr := CleanupPrePromptState(sessionID); cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to cleanup pre-prompt state: %v\n", cleanupErr)
		}
		return nil
	}

	logFileChanges(relModifiedFiles, relNewFiles, relDeletedFiles)

	commitMessage := generateCommitMessage(enriched.userPrompt)

	author, err := GetGitAuthor()
	if err != nil {
		return fmt.Errorf("failed to get git author: %w", err)
	}

	strat := GetStrategy()
	agentType := ag.Type()

	var tokenUsage *agent.TokenUsage
	if enriched.wireTokens != nil {
		tokenUsage = computeOpenCodeTokenDelta(enriched.wireTokens, sessionID)
	}

	var stepTranscriptStart int
	if preState != nil {
		stepTranscriptStart = preState.StepTranscriptStart
	}

	saveCtx := strategy.SaveContext{
		SessionID:           sessionID,
		ModifiedFiles:       relModifiedFiles,
		NewFiles:            relNewFiles,
		DeletedFiles:        relDeletedFiles,
		MetadataDir:         sessionDir,
		MetadataDirAbs:      sessionDirAbs,
		CommitMessage:       commitMessage,
		TranscriptPath:      transcriptPath,
		AuthorName:          author.Name,
		AuthorEmail:         author.Email,
		AgentType:           agentType,
		StepTranscriptStart: stepTranscriptStart,
		TokenUsage:          tokenUsage,
	}

	if err := strat.SaveChanges(saveCtx); err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	// Condensation reads state.TranscriptPath to find the full.jsonl — set it to the synthesized file
	if sessionState, loadErr := strategy.LoadSessionState(sessionID); loadErr == nil && sessionState != nil {
		sessionState.TranscriptPath = transcriptPath
		if updateErr := strategy.SaveSessionState(sessionState); updateErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to update session state transcript path: %v\n", updateErr)
		}
	}

	transitionSessionTurnEnd(sessionID)

	if cleanupErr := CleanupPrePromptState(sessionID); cleanupErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to cleanup pre-prompt state: %v\n", cleanupErr)
	}

	fmt.Fprintln(os.Stderr, "Session saved successfully")
	return nil
}

func handleOpenCodeSessionEnd() error {
	ag, err := agent.Get(agent.AgentNameOpenCode)
	if err != nil {
		return fmt.Errorf("failed to get opencode agent: %w", err)
	}

	input, err := ag.ParseHookInput(agent.HookSessionEnd, os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to parse hook input: %w", err)
	}

	logCtx := logging.WithAgent(logging.WithComponent(context.Background(), "hooks"), ag.Name())
	logging.Info(logCtx, "opencode-session-end",
		slog.String("hook", "session-end"),
		slog.String("hook_type", "agent"),
		slog.String("model_session_id", input.SessionID),
	)

	if input.SessionID == "" {
		return nil
	}

	if err := markSessionEnded(input.SessionID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to mark session ended: %v\n", err)
	}
	return nil
}

func handleOpenCodeAfterTool() error {
	logCtx := logging.WithComponent(context.Background(), "hooks")
	logging.Debug(logCtx, "opencode-after-tool (no-op)")
	return nil
}

func extractOpenCodeEnrichedData(input *agent.HookInput, ag agent.Agent, sessionID string, preState *PrePromptState) openCodeEnrichedData {
	var data openCodeEnrichedData

	if mf, ok := input.RawData["modified_files"].([]string); ok {
		data.modifiedFiles = mf
	}
	if t, ok := input.RawData["tokens"].(*opencode.WireToken); ok {
		data.wireTokens = t
	}
	if s, ok := input.RawData["summary"].(string); ok {
		data.summary = s
	}

	ocAgent, ok := ag.(*opencode.OpenCodeAgent)
	if !ok {
		ocAgent = &opencode.OpenCodeAgent{}
	}

	startOffset := 0
	if preState != nil {
		startOffset = preState.StartMessageIndex
	}

	if len(data.modifiedFiles) == 0 && data.wireTokens == nil {
		meta := opencode.ExtractSessionMetadata(sessionID, startOffset, ocAgent)
		data.modifiedFiles = meta.ModifiedFiles
		data.wireTokens = meta.Tokens
		if data.summary == "" {
			data.summary = meta.Summary
		}
	}

	data.userPrompt = extractOpenCodePrompt(sessionID, input, ocAgent)
	return data
}

func writeOpenCodeSessionMetadata(sessionDirAbs, sessionID string, enriched openCodeEnrichedData) error {
	if mkdirErr := os.MkdirAll(sessionDirAbs, 0o750); mkdirErr != nil { //nolint:gosec // path from controlled git metadata directory
		return fmt.Errorf("failed to create session directory: %w", mkdirErr)
	}

	ag, agErr := agent.Get(agent.AgentNameOpenCode)
	ocAgent, ok := ag.(*opencode.OpenCodeAgent)
	if agErr != nil || !ok {
		ocAgent = &opencode.OpenCodeAgent{}
	}

	transcriptPath := filepath.Join(sessionDirAbs, paths.TranscriptFileName)
	if txErr := opencode.SynthesizeTranscript(transcriptPath, sessionID, ocAgent); txErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to synthesize transcript: %v\n", txErr)
	}

	promptFile := filepath.Join(sessionDirAbs, paths.PromptFileName)
	if writeErr := os.WriteFile(promptFile, []byte(enriched.userPrompt), 0o600); writeErr != nil { //nolint:gosec // path from controlled git metadata directory
		fmt.Fprintf(os.Stderr, "Warning: failed to write prompt file: %v\n", writeErr)
	}

	summaryFile := filepath.Join(sessionDirAbs, paths.SummaryFileName)
	if writeErr := os.WriteFile(summaryFile, []byte(enriched.summary), 0o600); writeErr != nil { //nolint:gosec // path from controlled git metadata directory
		fmt.Fprintf(os.Stderr, "Warning: failed to write summary file: %v\n", writeErr)
	}

	commitMsg := generateCommitMessage(enriched.userPrompt)
	contextFile := filepath.Join(sessionDirAbs, paths.ContextFileName)
	if ctxErr := createContextFileForOpenCode(contextFile, commitMsg, sessionID, enriched.userPrompt, enriched.summary); ctxErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write context file: %v\n", ctxErr)
	}

	return nil
}

func computeOpenCodeTokenDelta(wireTokens *opencode.WireToken, sessionID string) *agent.TokenUsage {
	var prevBaseline *opencode.WireToken
	sessionState, stateErr := strategy.LoadSessionState(sessionID)
	if stateErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load session state for token baseline: %v\n", stateErr)
	}
	if sessionState != nil && sessionState.TokenUsage != nil {
		prevBaseline = &opencode.WireToken{
			Input:      sessionState.TokenUsage.InputTokens,
			Output:     sessionState.TokenUsage.OutputTokens,
			CacheRead:  sessionState.TokenUsage.CacheReadTokens,
			CacheWrite: sessionState.TokenUsage.CacheCreationTokens,
		}
	}
	return opencode.ComputePerTurnTokenUsage(wireTokens, prevBaseline)
}

func extractOpenCodePrompt(sessionID string, input *agent.HookInput, ag *opencode.OpenCodeAgent) string {
	if input.UserPrompt != "" {
		return input.UserPrompt
	}

	messages, err := ag.ReadMessagesFromStorage(sessionID)
	if err != nil || len(messages) == 0 {
		return ""
	}

	// Find the last user message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			parts, partsErr := ag.ReadPartsForMessage(messages[i].ID)
			if partsErr != nil {
				continue
			}
			var sb strings.Builder
			for _, part := range parts {
				if part.Type == "text" && part.Text != "" {
					if sb.Len() > 0 {
						sb.WriteString("\n")
					}
					sb.WriteString(part.Text)
				}
			}
			if sb.Len() > 0 {
				return sb.String()
			}
		}
	}
	return ""
}

func createContextFileForOpenCode(contextFile, commitMessage, sessionID, prompt, summary string) error {
	var sb strings.Builder

	sb.WriteString("# Session Context\n\n")
	sb.WriteString("Session ID: " + sessionID + "\n")
	sb.WriteString("Commit Message: " + commitMessage + "\n\n")

	if prompt != "" {
		sb.WriteString("## Prompt\n\n")
		sb.WriteString(prompt)
		sb.WriteString("\n\n")
	}

	if summary != "" {
		sb.WriteString("## Summary\n\n")
		sb.WriteString(summary)
		sb.WriteString("\n")
	}

	if err := os.WriteFile(contextFile, []byte(sb.String()), 0o600); err != nil { //nolint:gosec // path from controlled git metadata directory
		return fmt.Errorf("failed to write context file: %w", err)
	}
	return nil
}
