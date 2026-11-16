package main

import (
	"fmt"
	"os"

	"github.com/mauricebutts/motree/internal/hook"
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
	}
}
