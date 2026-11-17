package claudecmd

import (
	"os"
	"path/filepath"

	"github.com/mauricebutts/motree/internal/statusfile"
)

// ResumeCommand returns the appropriate Claude command for the given working directory.
// It returns "claude -c" if ~/.claude/projects/<escaped-cwd> exists and contains files,
// otherwise it returns "claude" to start a fresh conversation.
func ResumeCommand(cwd string) string {
	home, err := getHomeDir()
	if err != nil {
		return "claude"
	}

	projectDir := filepath.Join(home, ".claude", "projects", statusfile.EscapeCwd(cwd))

	// Check if directory exists and is non-empty
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		// Directory doesn't exist or can't be read
		return "claude"
	}

	// If directory has entries, return resume command
	if len(entries) > 0 {
		return "claude -c"
	}

	return "claude"
}

// getHomeDir returns the user's home directory
func getHomeDir() (string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return "", os.ErrNotExist
	}
	return home, nil
}
