//go:build server

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// normalizeLoadedDocument is the server-build boot normaliser (Spec 107
// FR-032/FR-033/FR-035). It runs on the RAW document before the typed decode:
// the removed server_edition keys and auth_broker leaves are dropped, the
// whole auth_broker block of a server using a never-implemented mode is
// dropped, and one LoadDiagnostic is recorded per drop (plus one for a
// `store_idp_tokens: true`, which is retained). A document with nothing to
// drop is returned untouched, so the common case never re-encodes the file.
//
// Numbers ride through the re-encode as json.Number, so their decimal text is
// preserved exactly; a boot never rewrites a value it did not drop.
func normalizeLoadedDocument(data []byte) ([]byte, []LoadDiagnostic, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	// Decoder.Decode stops after the first value. The strict json.Unmarshal
	// that follows on the untouched path refuses trailing content, and the
	// re-encoded path must be exactly as strict, or a file with a second
	// object / trailing garbage after a removed key would boot on the first
	// object alone.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected trailing content after the top-level object")
		}
		return nil, nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	diags := dropRemovedKeys(raw)
	if len(diags) == 0 {
		return data, nil, nil
	}
	// The deprecation-only case drops nothing; keep the original bytes.
	dropped := false
	for _, d := range diags {
		if d.Message != deprecatedStoreIDPTokensMessage {
			dropped = true
			break
		}
	}
	if !dropped {
		return data, diags, nil
	}

	normalized, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to re-encode normalised config: %w", err)
	}
	return normalized, diags, nil
}
