---
title: OpenCode Test Fixtures
---

# OpenCode Test Fixtures

Real transcript data captured from OpenCode sessions, sanitized and committed
as reference fixtures for schema verification tests.

## Fixtures

### real_session_multiturn.jsonl

- **Format**: JSONL (one JSON message per line)
- **Agent**: OpenCode v1.1.60
- **Capture date**: 2026-02-23
- **Messages**: 8 (2 user turns, 6 assistant messages)
- **Tools exercised**: `write`, `edit`, `glob`, `read`
- **Key schema detail**: OpenCode uses camelCase `filePath` in tool inputs
  (not snake_case `file_path` like Claude Code)

## How to capture new fixtures

1. Enable Entire with OpenCode in a test repo:
   ```
   entire enable --agent opencode
   ```

2. Run OpenCode with prompts that exercise file modification tools:
   ```
   ocode run 'Create a new file called test.txt with content: hello'
   ocode run 'Edit test.txt: change hello to goodbye'
   ```

3. Find the transcript file. Check the session state for the path:
   ```
   cat .git/entire-sessions/<session-id>.json | jq .transcript_path
   ```
   Transcripts are stored in `$TMPDIR/entire-opencode/<sanitized-path>/`.

4. Sanitize the transcript:
   - Replace absolute file paths with relative paths
   - Remove personal directory references (e.g., `/Users/<name>/`)
   - Remove large tool output noise (Agent Usage Reminders, README content)
   - Preserve JSON structure and key names exactly as-is

5. Verify the fixture:
   - Each line is valid JSON
   - `filePath` keys are camelCase (OpenCode's actual format)
   - No secrets, API keys, or personal paths remain
   - At least 2 user turns with file modification tools

## Key naming convention

OpenCode uses **camelCase** for tool input keys:

| Tool  | Key        | Example value       |
|-------|------------|---------------------|
| write | `filePath` | `"entire_test.txt"` |
| write | `content`  | `"file content\n"`  |
| edit  | `filePath` | `"entire_test.txt"` |
| edit  | `oldString`| `"old text"`        |
| edit  | `newString`| `"new text"`        |
| read  | `filePath` | `"entire_test.txt"` |
| glob  | `pattern`  | `"**/*.txt"`        |
| patch | `filePath` | `"entire_test.txt"` |

This differs from Claude Code which uses snake_case (`file_path`).
The `extractFilePathFromInput()` function must check both conventions.

## Known issues

**BUG: `extractFilePathFromInput()` does not check `filePath` (camelCase)**

As of this capture, `transcript.go:143` only checks `file_path`, `path`, `file`,
`filename` -- it does NOT include `filePath`. This means file extraction from real
OpenCode transcripts silently returns empty results. The fixture-based tests
(Task 5 in the plan) are expected to expose this gap.
