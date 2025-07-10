package prompt

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// UserPrompter handles user input prompting
type UserPrompter interface {
	// PromptString prompts the user for a string input
	PromptString(message string) (string, error)
	// PromptStringWithDefault prompts with a default value
	PromptStringWithDefault(message, defaultValue string) (string, error)
	// PromptSecret prompts for sensitive input (hidden)
	PromptSecret(message string) (string, error)
	// PromptConfirm prompts for yes/no confirmation
	PromptConfirm(message string) (bool, error)
}

// ConsolePrompter implements UserPrompter for console input
type ConsolePrompter struct {
	reader *bufio.Reader
}

// NewConsolePrompter creates a new console prompter
func NewConsolePrompter() *ConsolePrompter {
	return &ConsolePrompter{
		reader: bufio.NewReader(os.Stdin),
	}
}

// PromptString prompts the user for a string input
func (p *ConsolePrompter) PromptString(message string) (string, error) {
	fmt.Print(message)
	input, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(input), nil
}

// PromptStringWithDefault prompts with a default value
func (p *ConsolePrompter) PromptStringWithDefault(message, defaultValue string) (string, error) {
	prompt := fmt.Sprintf("%s [%s]: ", message, defaultValue)
	input, err := p.PromptString(prompt)
	if err != nil {
		return "", err
	}
	if input == "" {
		return defaultValue, nil
	}
	return input, nil
}

// PromptSecret prompts for sensitive input (hidden)
func (p *ConsolePrompter) PromptSecret(message string) (string, error) {
	fmt.Print(message)

	// Check if stdin is a terminal
	if !term.IsTerminal(syscall.Stdin) {
		// Not a terminal, read normally
		input, err := p.reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(input), nil
	}

	// Terminal input - hide the input
	password, err := term.ReadPassword(syscall.Stdin)
	if err != nil {
		return "", err
	}
	fmt.Println() // Add newline after hidden input
	return string(password), nil
}

// PromptConfirm prompts for yes/no confirmation
func (p *ConsolePrompter) PromptConfirm(message string) (bool, error) {
	prompt := fmt.Sprintf("%s [y/N]: ", message)
	input, err := p.PromptString(prompt)
	if err != nil {
		return false, err
	}

	input = strings.ToLower(strings.TrimSpace(input))
	return input == "y" || input == "yes", nil
}

// MockPrompter implements UserPrompter for testing
type MockPrompter struct {
	responses map[string]string
	confirms  map[string]bool
}

// NewMockPrompter creates a new mock prompter for testing
func NewMockPrompter() *MockPrompter {
	return &MockPrompter{
		responses: make(map[string]string),
		confirms:  make(map[string]bool),
	}
}

// SetResponse sets a response for a given message
func (m *MockPrompter) SetResponse(message, response string) {
	m.responses[message] = response
}

// SetConfirm sets a confirmation response for a given message
func (m *MockPrompter) SetConfirm(message string, confirm bool) {
	m.confirms[message] = confirm
}

// PromptString returns the pre-set response
func (m *MockPrompter) PromptString(message string) (string, error) {
	if response, exists := m.responses[message]; exists {
		return response, nil
	}
	return "", fmt.Errorf("no response set for message: %s", message)
}

// PromptStringWithDefault returns the pre-set response or default
func (m *MockPrompter) PromptStringWithDefault(message, defaultValue string) (string, error) {
	if response, exists := m.responses[message]; exists {
		if response == "" {
			return defaultValue, nil
		}
		return response, nil
	}
	return defaultValue, nil
}

// PromptSecret returns the pre-set response
func (m *MockPrompter) PromptSecret(message string) (string, error) {
	if response, exists := m.responses[message]; exists {
		return response, nil
	}
	return "", fmt.Errorf("no response set for message: %s", message)
}

// PromptConfirm returns the pre-set confirmation
func (m *MockPrompter) PromptConfirm(message string) (bool, error) {
	if confirm, exists := m.confirms[message]; exists {
		return confirm, nil
	}
	return false, nil
}
