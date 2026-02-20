package opencode

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

var (
	_ agent.HookSupport = (*OpenCodeAgent)(nil)
	_ agent.HookHandler = (*OpenCodeAgent)(nil)
)

const (
	HookNameSessionStart = "session-start"
	HookNameSessionEnd   = "session-end"
	HookNameBeforeAgent  = "before-agent"
	HookNameAfterAgent   = "after-agent"
	HookNameAfterTool    = "after-tool"
)

// GetHookNames returns the hook verbs OpenCode supports.
// These become subcommands: entire hooks opencode <verb>
func (o *OpenCodeAgent) GetHookNames() []string {
	return []string{
		HookNameSessionStart,
		HookNameSessionEnd,
		HookNameBeforeAgent,
		HookNameAfterAgent,
		HookNameAfterTool,
	}
}

// InstallHooks writes the Entire plugin to .opencode/plugins/entire.ts.
func (o *OpenCodeAgent) InstallHooks(_ bool, _ bool) (int, error) {
	repoRoot, err := paths.RepoRoot()
	if err != nil {
		repoRoot = "."
	}

	pluginDir := filepath.Join(repoRoot, ".opencode", "plugins")
	if err := os.MkdirAll(pluginDir, 0o750); err != nil {
		return 0, fmt.Errorf("failed to create plugins directory: %w", err)
	}

	pluginPath := filepath.Join(pluginDir, "entire.ts")
	if err := os.WriteFile(pluginPath, []byte(entirePluginSource), 0o600); err != nil {
		return 0, fmt.Errorf("failed to write plugin file: %w", err)
	}

	return 5, nil // 5 hooks: session-start, session-end, before-agent, after-agent, after-tool
}

func (o *OpenCodeAgent) UninstallHooks() error {
	repoRoot, err := paths.RepoRoot()
	if err != nil {
		repoRoot = "."
	}

	pluginPath := filepath.Join(repoRoot, ".opencode", "plugins", "entire.ts")
	if err := os.Remove(pluginPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plugin file: %w", err)
	}
	return nil
}

func (o *OpenCodeAgent) AreHooksInstalled() bool {
	repoRoot, err := paths.RepoRoot()
	if err != nil {
		repoRoot = "."
	}

	pluginPath := filepath.Join(repoRoot, ".opencode", "plugins", "entire.ts")
	_, err = os.Stat(pluginPath)
	return err == nil
}

// entirePluginSource is the TypeScript plugin that bridges OpenCode events to Entire CLI hooks.
// This is the "fat plugin" approach per the proposal: the after-agent hook sends enriched
// metadata (modified files, tokens, cost, summary) collected via the SDK client.
// This decouples the Go agent from OpenCode's storage layout.
// On SDK failure, the plugin falls back to a thin {session_id} payload.
const entirePluginSource = `import { execFileSync } from "child_process"

const FILE_TOOLS = new Set(["edit", "write", "patch", "multiedit"])

function run(verb, payload) {
  try {
    execFileSync("entire", ["hooks", "opencode", verb], {
      input: JSON.stringify(payload),
      timeout: 10000,
      stdio: ["pipe", "pipe", "pipe"],
    })
  } catch (error) {
    const msg = error instanceof Error ? error.message : String(error)
    process.stderr.write(` + "`[entire-plugin] ${verb} failed: ${msg}\\n`" + `)
  }
}

// Collect session metadata via SDK client (localhost HTTP to in-process server).
// Returns enriched fields for the after-agent payload.
// On any failure, returns empty object so plugin falls back to thin payload.
async function collectMetadata(client, sessionID) {
  try {
    const { data: messages } = await client.session.messages({
      path: { id: sessionID },
    })
    if (!messages) return {}

    const modifiedFiles = []
    let totalCost = 0
    const tokens = { input: 0, output: 0, reasoning: 0, cache_read: 0, cache_write: 0 }
    let summary = ""

    for (const msg of messages) {
      if (msg.info.role === "assistant") {
        totalCost += msg.info.cost
        tokens.input += msg.info.tokens.input
        tokens.output += msg.info.tokens.output
        tokens.reasoning += msg.info.tokens.reasoning
        tokens.cache_read += msg.info.tokens.cache.read
        tokens.cache_write += msg.info.tokens.cache.write
      }

      for (const part of msg.parts) {
        if (
          part.type === "tool" &&
          FILE_TOOLS.has(part.tool) &&
          part.state.status === "completed" &&
          part.state.input?.file_path
        ) {
          const fp = String(part.state.input.file_path)
          if (!modifiedFiles.includes(fp)) modifiedFiles.push(fp)
        }

        if (part.type === "text" && msg.info.role === "assistant") {
          summary = part.text
        }
      }
    }

    return { modified_files: modifiedFiles, tokens, cost: totalCost, summary }
  } catch {
    return {}
  }
}

export default async ({ client }) => ({
  event: async ({ event }) => {
    if (event.type === "session.created") {
      run("session-start", { session_id: event.properties.info.id })
      return
    }

    if (event.type === "session.status" && event.properties.status?.type === "idle") {
      const metadata = await collectMetadata(client, event.properties.sessionID)
      run("after-agent", { session_id: event.properties.sessionID, ...metadata })
      return
    }

    if (event.type === "session.deleted") {
      run("session-end", { session_id: event.properties.info.id })
      return
    }

    if (event.type === "session.updated" && event.properties.info?.time?.archived) {
      run("session-end", { session_id: event.properties.info.id })
    }
  },

  "chat.message": async (input, output) => {
    const parts = Array.isArray(output?.parts) ? output.parts : output?.message?.parts
    if (!Array.isArray(parts)) {
      run("before-agent", { session_id: input.sessionID, prompt: "" })
      return
    }
    const text = parts
      .filter((p) => p && typeof p === "object" && p.type === "text" && typeof p.text === "string")
      .map((p) => p.text)
      .join("\\n")
    run("before-agent", { session_id: input.sessionID, prompt: text })
  },

  "tool.execute.after": async (input) => {
    if (!FILE_TOOLS.has(input.tool)) return
    run("after-tool", {
      session_id: input.sessionID,
      tool_name: input.tool,
      tool_use_id: input.callID,
      tool_input: input.args,
    })
  },
})
`
