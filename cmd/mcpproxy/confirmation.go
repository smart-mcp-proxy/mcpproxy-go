package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// confirmBulkAction prompts user for confirmation when performing bulk operations.
// Returns (true, nil) if user confirms or force=true
// Returns (false, nil) if user declines
// Returns (false, error) if non-interactive without force flag
func confirmBulkAction(action string, count int, force bool) (bool, error) {
	// Skip prompt if force flag provided
	if force {
		return true, nil
	}

	// Check if stdin is a TTY (interactive terminal)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, fmt.Errorf("--all requires --force flag in non-interactive mode")
	}

	// Show confirmation prompt
	fmt.Printf("⚠️  This will %s %d server(s). Continue? [y/N]: ", action, count)

	// Read user input
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read confirmation: %w", err)
	}

	// Parse response (accept y, yes case-insensitive)
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes", nil
}
