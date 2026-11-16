package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mauricebutts/motree/internal/hook"
	"github.com/mauricebutts/motree/internal/hookinstall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mt [command] [args...]\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "__hook":
		// Hook command always exits 0, never surfaces errors to Claude Code
		if err := hook.Run(os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "hook error: %v\n", err)
		}
		os.Exit(0)
	case "install-hooks":
		if err := runInstallHooks(); err != nil {
			fmt.Fprintf(os.Stderr, "install-hooks error: %v\n", err)
			os.Exit(1)
		}
	}
}

// runInstallHooks registers mt's "__hook" subcommand with Claude Code by
// merging it into the user's ~/.claude/settings.json.
func runInstallHooks() error {
	mtBinaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve mt binary path: %w", err)
	}

	home := os.Getenv("HOME")
	if home == "" {
		return fmt.Errorf("HOME environment variable is not set")
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	if err := hookinstall.Merge(settingsPath, mtBinaryPath); err != nil {
		return err
	}

	fmt.Printf("Installed mt hooks into %s\n", settingsPath)
	return nil
}
