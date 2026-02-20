package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// StoragePath returns the OpenCode storage directory.
// Follows XDG spec: $OPENCODE_HOME > $XDG_DATA_HOME/opencode > platform default.
func StoragePath() string {
	if home := os.Getenv("OPENCODE_HOME"); home != "" {
		return filepath.Join(home, "storage")
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode", "storage")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".opencode", "storage")
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(homeDir, "Library", "Application Support", "opencode", "storage")
	}
	return filepath.Join(homeDir, ".local", "share", "opencode", "storage")
}

// FindProjectID scans OpenCode project files to find the project matching repoRoot.
func FindProjectID(repoRoot string) (string, error) {
	projectDir := filepath.Join(StoragePath(), "project")
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "", fmt.Errorf("failed to read project dir: %w", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(projectDir, entry.Name())) //nolint:gosec // path from controlled OpenCode storage directory
		if readErr != nil {
			continue
		}
		var proj Project
		if jsonErr := json.Unmarshal(data, &proj); jsonErr != nil {
			continue
		}
		if proj.Directory == repoRoot {
			return proj.ID, nil
		}
	}
	return "", nil
}
