package secret

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// NewResolver creates a new secret resolver
func NewResolver() *Resolver {
	r := &Resolver{
		providers: make(map[string]Provider),
	}

	// Register default providers
	r.RegisterProvider("env", NewEnvProvider())
	r.RegisterProvider("keyring", NewKeyringProvider())

	return r
}

// RegisterProvider registers a new secret provider
func (r *Resolver) RegisterProvider(secretType string, provider Provider) {
	r.providers[secretType] = provider
}

// Resolve resolves a single secret reference
func (r *Resolver) Resolve(ctx context.Context, ref Ref) (string, error) {
	provider, exists := r.providers[ref.Type]
	if !exists {
		return "", fmt.Errorf("no provider for secret type: %s", ref.Type)
	}

	if !provider.CanResolve(ref.Type) {
		return "", fmt.Errorf("provider cannot resolve secret type: %s", ref.Type)
	}

	if !provider.IsAvailable() {
		return "", fmt.Errorf("provider for %s is not available on this system", ref.Type)
	}

	return provider.Resolve(ctx, ref)
}

// Store stores a secret using the appropriate provider
func (r *Resolver) Store(ctx context.Context, ref Ref, value string) error {
	provider, exists := r.providers[ref.Type]
	if !exists {
		return fmt.Errorf("no provider for secret type: %s", ref.Type)
	}

	if !provider.IsAvailable() {
		return fmt.Errorf("provider for %s is not available on this system", ref.Type)
	}

	return provider.Store(ctx, ref, value)
}

// Delete deletes a secret using the appropriate provider
func (r *Resolver) Delete(ctx context.Context, ref Ref) error {
	provider, exists := r.providers[ref.Type]
	if !exists {
		return fmt.Errorf("no provider for secret type: %s", ref.Type)
	}

	if !provider.IsAvailable() {
		return fmt.Errorf("provider for %s is not available on this system", ref.Type)
	}

	return provider.Delete(ctx, ref)
}

// ListAll lists all secret references from all providers
func (r *Resolver) ListAll(ctx context.Context) ([]Ref, error) {
	var allRefs []Ref

	for _, provider := range r.providers {
		if !provider.IsAvailable() {
			continue
		}

		refs, err := provider.List(ctx)
		if err != nil {
			// Log error but continue with other providers
			continue
		}

		allRefs = append(allRefs, refs...)
	}

	return allRefs, nil
}

// GetAvailableProviders returns a list of available providers
func (r *Resolver) GetAvailableProviders() []string {
	var available []string
	for secretType, provider := range r.providers {
		if provider.IsAvailable() {
			available = append(available, secretType)
		}
	}
	return available
}

// ExpandStructSecrets recursively expands secret references in a struct
func (r *Resolver) ExpandStructSecrets(ctx context.Context, v interface{}) error {
	return r.expandValue(ctx, reflect.ValueOf(v))
}

// expandValue recursively processes a reflect.Value
func (r *Resolver) expandValue(ctx context.Context, v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}

	// Handle pointers
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		return r.expandValue(ctx, v.Elem())
	}

	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			original := v.String()
			if IsSecretRef(original) {
				expanded, err := r.ExpandSecretRefs(ctx, original)
				if err != nil {
					return err
				}
				v.SetString(expanded)
			}
		}

	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if field.CanInterface() {
				if err := r.expandValue(ctx, field); err != nil {
					return err
				}
			}
		}

	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := r.expandValue(ctx, v.Index(i)); err != nil {
				return err
			}
		}

	case reflect.Map:
		for _, key := range v.MapKeys() {
			mapValue := v.MapIndex(key)
			if mapValue.Kind() == reflect.String && IsSecretRef(mapValue.String()) {
				expanded, err := r.ExpandSecretRefs(ctx, mapValue.String())
				if err != nil {
					return err
				}
				newValue := reflect.ValueOf(expanded)
				v.SetMapIndex(key, newValue)
			} else if mapValue.Kind() == reflect.Interface {
				// Handle interface{} values
				actualValue := mapValue.Elem()
				if actualValue.Kind() == reflect.String && IsSecretRef(actualValue.String()) {
					expanded, err := r.ExpandSecretRefs(ctx, actualValue.String())
					if err != nil {
						return err
					}
					v.SetMapIndex(key, reflect.ValueOf(expanded))
				}
			}
		}
	}

	return nil
}

// ExtractConfigSecrets extracts all secret and environment references from a config structure
func (r *Resolver) ExtractConfigSecrets(ctx context.Context, v interface{}) (*ConfigSecretsResponse, error) {
	allRefs := []Ref{}
	r.extractSecretRefs(reflect.ValueOf(v), "", &allRefs)

	// Separate secrets by type
	var keyringRefs []Ref
	var envRefs []Ref
	var envStatus []EnvVarStatus

	for _, ref := range allRefs {
		switch ref.Type {
		case "keyring":
			keyringRefs = append(keyringRefs, ref)
		case "env":
			envRefs = append(envRefs, ref)

			// Check if environment variable exists
			provider, exists := r.providers["env"]
			isSet := false
			if exists && provider.IsAvailable() {
				_, err := provider.Resolve(ctx, ref)
				isSet = err == nil
			}

			envStatus = append(envStatus, EnvVarStatus{
				Ref: ref,
				IsSet:     isSet,
			})
		}
	}

	return &ConfigSecretsResponse{
		Secrets:         keyringRefs,
		EnvironmentVars: envStatus,
		TotalSecrets:    len(keyringRefs),
		TotalEnvVars:    len(envRefs),
	}, nil
}

// extractSecretRefs recursively extracts secret references from a struct
func (r *Resolver) extractSecretRefs(v reflect.Value, path string, refs *[]Ref) {
	if !v.IsValid() {
		return
	}

	// Handle pointers
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return
		}
		r.extractSecretRefs(v.Elem(), path, refs)
		return
	}

	switch v.Kind() {
	case reflect.String:
		value := v.String()
		if value != "" && IsSecretRef(value) {
			if secretRef, err := ParseSecretRef(value); err == nil {
				*refs = append(*refs, *secretRef)
			}
		}

	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if !field.CanInterface() {
				continue
			}

			fieldType := t.Field(i)
			fieldName := fieldType.Name

			// Skip unexported fields
			if !fieldType.IsExported() {
				continue
			}

			newPath := fieldName
			if path != "" {
				newPath = path + "." + fieldName
			}

			r.extractSecretRefs(field, newPath, refs)
		}

	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			newPath := fmt.Sprintf("%s[%d]", path, i)
			r.extractSecretRefs(v.Index(i), newPath, refs)
		}

	case reflect.Map:
		for _, key := range v.MapKeys() {
			keyStr := fmt.Sprintf("%v", key.Interface())
			newPath := fmt.Sprintf("%s[%s]", path, keyStr)
			r.extractSecretRefs(v.MapIndex(key), newPath, refs)
		}
	}
}

// AnalyzeForMigration analyzes a struct for potential secrets that could be migrated
func (r *Resolver) AnalyzeForMigration(v interface{}) *MigrationAnalysis {
	candidates := []MigrationCandidate{}
	r.analyzeValue(reflect.ValueOf(v), "", &candidates)

	return &MigrationAnalysis{
		Candidates: candidates,
		TotalFound: len(candidates),
	}
}

// analyzeValue recursively analyzes a reflect.Value for potential secrets
func (r *Resolver) analyzeValue(v reflect.Value, path string, candidates *[]MigrationCandidate) {
	if !v.IsValid() {
		return
	}

	// Handle pointers
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return
		}
		r.analyzeValue(v.Elem(), path, candidates)
		return
	}

	switch v.Kind() {
	case reflect.String:
		value := v.String()
		if value != "" && !IsSecretRef(value) {
			isSecret, confidence := DetectPotentialSecret(value, path)
			if isSecret {
				// Suggest keyring for most secrets
				suggestedRef := fmt.Sprintf("${keyring:%s}", r.generateSecretName(path))

				*candidates = append(*candidates, MigrationCandidate{
					Field:      path,
					Value:      MaskSecretValue(value),
					Suggested:  suggestedRef,
					Confidence: confidence,
				})
			}
		}

	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			fieldType := t.Field(i)

			if field.CanInterface() {
				fieldPath := path
				if fieldPath != "" {
					fieldPath += "."
				}
				fieldPath += fieldType.Name

				r.analyzeValue(field, fieldPath, candidates)
			}
		}

	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			indexPath := fmt.Sprintf("%s[%d]", path, i)
			r.analyzeValue(v.Index(i), indexPath, candidates)
		}

	case reflect.Map:
		for _, key := range v.MapKeys() {
			keyStr := fmt.Sprintf("%v", key.Interface())
			mapPath := path
			if mapPath != "" {
				mapPath += "."
			}
			mapPath += keyStr

			r.analyzeValue(v.MapIndex(key), mapPath, candidates)
		}
	}
}

// generateSecretName generates a keyring secret name from a field path
func (r *Resolver) generateSecretName(fieldPath string) string {
	// Convert field path to a reasonable secret name
	name := strings.ToLower(fieldPath)
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, "[", "_")
	name = strings.ReplaceAll(name, "]", "")

	// Remove common prefixes to make names shorter
	prefixes := []string{"serverconfig_", "config_", "oauth_"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
			break
		}
	}

	return name
}
