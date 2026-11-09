package secret

import (
	"context"
	"errors"
	"fmt"
)

const (
	// SecretTypeFile is the type of a ${file:/absolute/path} reference.
	SecretTypeFile = "file"
)

// FileProvider resolves ${file:/absolute/path} references from files, the way
// Docker Compose and Swarm secrets (/run/secrets/<name>), Kubernetes secret
// volumes and systemd LoadCredential= deliver a secret. The read rules (absolute
// path, regular file, size cap, one trailing newline removed, no NUL) are those
// of ReadFile.
//
// The file is read each time a reference is resolved; nothing is cached or logged.
type FileProvider struct{}

// NewFileProvider creates a new file provider.
func NewFileProvider() *FileProvider {
	return &FileProvider{}
}

// CanResolve returns true if this provider can handle the given secret type.
func (p *FileProvider) CanResolve(secretType string) bool {
	return secretType == SecretTypeFile
}

// Resolve reads the secret from the file named by the reference. An empty file
// is an error, as an unset or empty variable is for the env provider: a secret
// that resolves to nothing would silently send an empty credential.
func (p *FileProvider) Resolve(_ context.Context, ref Ref) (string, error) {
	if !p.CanResolve(ref.Type) {
		return "", fmt.Errorf("file provider cannot resolve secret type: %s", ref.Type)
	}
	value, err := ReadFile(ref.Name)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("secret file %q is empty", ref.Name)
	}
	return value, nil
}

// Store is not supported: mounted secret files are owned by the orchestrator.
func (p *FileProvider) Store(_ context.Context, _ Ref, _ string) error {
	return errors.New("file provider does not support storing secrets")
}

// Delete is not supported.
func (p *FileProvider) Delete(_ context.Context, _ Ref) error {
	return errors.New("file provider does not support deleting secrets")
}

// List returns nothing: there is no registry of secret files to enumerate.
func (p *FileProvider) List(_ context.Context) ([]Ref, error) {
	return nil, nil
}

// IsAvailable always returns true; whether a given file exists is reported when
// a reference to it is resolved.
func (p *FileProvider) IsAvailable() bool {
	return true
}
