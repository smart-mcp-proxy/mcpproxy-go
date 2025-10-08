package tokens

import (
	"encoding/json"
	"fmt"
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
	"go.uber.org/zap"
)

// Tokenizer provides token counting functionality for various LLM models
type Tokenizer interface {
	// CountTokens counts tokens in text using the default encoding
	CountTokens(text string) (int, error)

	// CountTokensForModel counts tokens for a specific model
	CountTokensForModel(text string, model string) (int, error)

	// CountTokensForEncoding counts tokens using a specific encoding
	CountTokensForEncoding(text string, encoding string) (int, error)

	// CountTokensInJSON counts tokens in a JSON object (serialized first)
	CountTokensInJSON(data interface{}) (int, error)

	// CountTokensInJSONForModel counts tokens in JSON for a specific model
	CountTokensInJSONForModel(data interface{}, model string) (int, error)
}

// DefaultTokenizer implements the Tokenizer interface using tiktoken-go
type DefaultTokenizer struct {
	defaultEncoding string
	encodingCache   map[string]*tiktoken.Tiktoken
	mu              sync.RWMutex
	logger          *zap.SugaredLogger
	enabled         bool
}

// NewTokenizer creates a new tokenizer instance
func NewTokenizer(defaultEncoding string, logger *zap.SugaredLogger, enabled bool) (*DefaultTokenizer, error) {
	if defaultEncoding == "" {
		defaultEncoding = DefaultEncoding
	}

	// Validate encoding exists
	_, err := tiktoken.GetEncoding(defaultEncoding)
	if err != nil {
		return nil, fmt.Errorf("invalid encoding %q: %w", defaultEncoding, err)
	}

	return &DefaultTokenizer{
		defaultEncoding: defaultEncoding,
		encodingCache:   make(map[string]*tiktoken.Tiktoken),
		logger:          logger,
		enabled:         enabled,
	}, nil
}

// getEncoding retrieves or caches a tiktoken encoding
func (t *DefaultTokenizer) getEncoding(encoding string) (*tiktoken.Tiktoken, error) {
	t.mu.RLock()
	if enc, ok := t.encodingCache[encoding]; ok {
		t.mu.RUnlock()
		return enc, nil
	}
	t.mu.RUnlock()

	// Not in cache, acquire write lock and load
	t.mu.Lock()
	defer t.mu.Unlock()

	// Double-check after acquiring write lock
	if enc, ok := t.encodingCache[encoding]; ok {
		return enc, nil
	}

	enc, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		return nil, fmt.Errorf("failed to get encoding %q: %w", encoding, err)
	}

	t.encodingCache[encoding] = enc
	return enc, nil
}

// CountTokens counts tokens using the default encoding
func (t *DefaultTokenizer) CountTokens(text string) (int, error) {
	if !t.enabled {
		return 0, nil
	}

	return t.CountTokensForEncoding(text, t.defaultEncoding)
}

// CountTokensForModel counts tokens for a specific model
func (t *DefaultTokenizer) CountTokensForModel(text string, model string) (int, error) {
	if !t.enabled {
		return 0, nil
	}

	encoding := GetEncodingForModel(model)

	// Log if using Claude model (approximation warning)
	if IsClaudeModel(model) && t.logger != nil {
		t.logger.Debugf("Using approximate token count for Claude model %q (cl100k_base encoding)", model)
	}

	return t.CountTokensForEncoding(text, encoding)
}

// CountTokensForEncoding counts tokens using a specific encoding
func (t *DefaultTokenizer) CountTokensForEncoding(text string, encoding string) (int, error) {
	if !t.enabled {
		return 0, nil
	}

	enc, err := t.getEncoding(encoding)
	if err != nil {
		return 0, err
	}

	tokens := enc.Encode(text, nil, nil)
	return len(tokens), nil
}

// CountTokensInJSON serializes data to JSON and counts tokens
func (t *DefaultTokenizer) CountTokensInJSON(data interface{}) (int, error) {
	if !t.enabled {
		return 0, nil
	}

	return t.CountTokensInJSONForEncoding(data, t.defaultEncoding)
}

// CountTokensInJSONForModel serializes data to JSON and counts tokens for a model
func (t *DefaultTokenizer) CountTokensInJSONForModel(data interface{}, model string) (int, error) {
	if !t.enabled {
		return 0, nil
	}

	encoding := GetEncodingForModel(model)
	return t.CountTokensInJSONForEncoding(data, encoding)
}

// CountTokensInJSONForEncoding serializes data to JSON and counts tokens
func (t *DefaultTokenizer) CountTokensInJSONForEncoding(data interface{}, encoding string) (int, error) {
	if !t.enabled {
		return 0, nil
	}

	// Serialize to JSON
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal data to JSON: %w", err)
	}

	return t.CountTokensForEncoding(string(jsonBytes), encoding)
}

// SetEnabled enables or disables token counting
func (t *DefaultTokenizer) SetEnabled(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = enabled
}

// IsEnabled returns whether token counting is enabled
func (t *DefaultTokenizer) IsEnabled() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.enabled
}

// SetDefaultEncoding changes the default encoding
func (t *DefaultTokenizer) SetDefaultEncoding(encoding string) error {
	// Validate encoding exists
	_, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		return fmt.Errorf("invalid encoding %q: %w", encoding, err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.defaultEncoding = encoding
	return nil
}

// GetDefaultEncoding returns the current default encoding
func (t *DefaultTokenizer) GetDefaultEncoding() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.defaultEncoding
}
