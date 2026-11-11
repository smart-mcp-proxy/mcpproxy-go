package management

import (
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 FR-030: the OAuth token storage section of doctor.

// Warning codes of contracts.OAuthTokenStorageReport.Warnings.
const (
	OAuthStorageWarnLiteralKey       = "literal_key"
	OAuthStorageWarnKeyFileInDataDir = "key_file_in_data_dir"
	OAuthStorageWarnPlaintextWithKey = "plaintext_rows_with_key"
	OAuthStorageWarnCompaction       = "compaction_advised"
	OAuthStorageWarnBackupPresent    = "backup_file_present"
)

// OAuthTokenStorageState is the raw material of the section, read by the
// runtime (or, for `auth tokens status`, by the CLI) at call time. It holds
// the public key id and key-source metadata only: never the key, never a path.
type OAuthTokenStorageState struct {
	// KeyID is the configured key's public id; "" when encryption is off.
	KeyID string
	// KeySource is what the key resolution recorded (kind, inside data dir).
	KeySource config.KeySource
	// Rows classifies every oauth_tokens row.
	Rows []storage.OAuthTokenRow
	// CompactAdvised is the compaction-advised marker.
	CompactAdvised bool
	// BackupPresent is true when config.db.bak exists in the data directory.
	BackupPresent bool
}

// OAuthTokenStorageOperations is implemented by the runtime. The management
// service reads it through an optional interface so a runtime that cannot
// report (a test double) simply leaves the section out.
type OAuthTokenStorageOperations interface {
	OAuthTokenStorageState() (OAuthTokenStorageState, error)
}

// OAuthRowServer names the server of an oauth_tokens bucket key, falling back
// to the bucket key itself when it has no "<name>_<hash>" shape.
func OAuthRowServer(bucketKey string) string {
	if name := storage.OAuthKeyServerName(bucketKey); name != "" {
		return name
	}
	return bucketKey
}

// BuildOAuthTokenStorageReport computes the section from st. visible filters
// the rows to the servers the caller may see (nil = all); admin gates the key
// id, the key-source metadata, the row key ids and every warning: a scoped
// caller sees counts over its visible servers only (Spec 118 scoping).
func BuildOAuthTokenStorageReport(st OAuthTokenStorageState, visible func(server string) bool, admin bool) *contracts.OAuthTokenStorageReport {
	rep := &contracts.OAuthTokenStorageReport{
		EncryptionEnabled: st.KeyID != "",
		Servers:           []contracts.OAuthTokenStorageServer{},
		Warnings:          []string{},
	}
	for _, row := range st.Rows {
		server := OAuthRowServer(row.BucketKey)
		if visible != nil && !visible(server) {
			continue
		}
		switch row.Class {
		case storage.OAuthRowPlaintext:
			rep.Plaintext++
		case storage.OAuthRowSealed:
			rep.Sealed++
		default:
			rep.Undecryptable++
			switch row.Reason {
			case tokenseal.ReasonNoKey:
				rep.UndecryptableByReason.NoKey++
			case tokenseal.ReasonKeyMismatch:
				rep.UndecryptableByReason.KeyMismatch++
			case tokenseal.ReasonAuthFailed:
				rep.UndecryptableByReason.AuthFailed++
			case tokenseal.ReasonUnsupportedVersion:
				rep.UndecryptableByReason.UnsupportedVersion++
			default:
				rep.UndecryptableByReason.Malformed++
			}
			entry := contracts.OAuthTokenStorageServer{
				BucketKey: row.BucketKey,
				Server:    server,
				Reason:    string(row.Reason),
			}
			if entry.Reason == "" {
				entry.Reason = string(tokenseal.ReasonMalformed)
			}
			if admin {
				entry.RowKeyID = row.RowKID
			}
			rep.Servers = append(rep.Servers, entry)
		}
	}
	if !admin {
		return rep
	}
	rep.KeyID = st.KeyID
	if rep.EncryptionEnabled {
		rep.KeySourceKind = string(st.KeySource.Kind)
		rep.KeySourceInDataDir = st.KeySource.InDataDir
		if st.KeySource.Kind == config.KeySourceLiteral {
			rep.Warnings = append(rep.Warnings, OAuthStorageWarnLiteralKey)
		}
		if st.KeySource.InDataDir {
			rep.Warnings = append(rep.Warnings, OAuthStorageWarnKeyFileInDataDir)
		}
		if rep.Plaintext > 0 {
			rep.Warnings = append(rep.Warnings, OAuthStorageWarnPlaintextWithKey)
		}
		if st.BackupPresent {
			rep.Warnings = append(rep.Warnings, OAuthStorageWarnBackupPresent)
		}
	}
	rep.CompactionAdvised = st.CompactAdvised
	if st.CompactAdvised {
		rep.Warnings = append(rep.Warnings, OAuthStorageWarnCompaction)
	}
	return rep
}

// oauthTokenStorageWarningLine is the runtime-warning sentence for one
// undecryptable server. It names the reason and the next step, never key
// material.
func oauthTokenStorageWarningLine(s contracts.OAuthTokenStorageServer, admin bool) string {
	msg := fmt.Sprintf("OAuth token for server %q cannot be decrypted (%s)", s.Server, s.Reason)
	if admin && s.RowKeyID != "" {
		msg += fmt.Sprintf(", sealed with key id %s", s.RowKeyID)
	}
	return msg + ". Restore the encryption key, or run: mcpproxy auth logout --server=" + s.Server
}
