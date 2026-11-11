package storage

import (
	"context"
	stderrors "errors"
	"fmt"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 PR-3: storage support for the offline `mcpproxy auth tokens`
// commands and for doctor. Nothing here returns a record field, key bytes or
// row bytes; rows are reported by bucket key, class, reason and key id only.

// Reasons a plaintext row is left as it is by the sweep.
const (
	// OAuthSkipMalformed: the plaintext value is not a valid record.
	OAuthSkipMalformed = "malformed"
	// OAuthSkipTooLarge: the plaintext record is over tokenseal.MaxPlaintextLen.
	OAuthSkipTooLarge = "too_large"
)

// OAuthSkippedRow names a plaintext row the sweep left unsealed and why.
type OAuthSkippedRow struct {
	BucketKey string
	Reason    string // OAuthSkipMalformed or OAuthSkipTooLarge
}

// OAuthSealReport is the outcome of SealOAuthTokens.
type OAuthSealReport struct {
	// Sealed is the number of rows sealed (rows a dry run would seal).
	Sealed int
	// Skipped lists the plaintext rows left as they are, in key order.
	Skipped []OAuthSkippedRow
}

// OAuthDecryptReport is the outcome of DecryptOAuthTokens.
type OAuthDecryptReport struct {
	// Decrypted is the number of sealed rows rewritten as plaintext.
	Decrypted int
}

// errOAuthDryRun aborts the sweep's transaction after it counted its work.
var errOAuthDryRun = stderrors.New("dry run")

// OAuthKeyServerName returns the server name encoded in an oauth_tokens
// bucket key ("<name>_<16 hex>"), or "" when the key has no such shape.
func OAuthKeyServerName(bucketKey string) string {
	return extractServerNameFromKey(bucketKey)
}

// SealOAuthTokens is the sweep behind SealPlaintextOAuthTokens with a report.
// With dryRun it does the whole walk and the writes inside the transaction and
// then rolls it back, so the counts are exactly what a real run would produce
// and nothing (not even the compaction marker) is persisted. Without a key it
// does nothing.
func (b *BoltDB) SealOAuthTokens(ctx context.Context, dryRun bool) (OAuthSealReport, error) {
	var rep OAuthSealReport
	if b.oauthKey == nil {
		return rep, nil
	}
	err := b.db.Update(func(tx *bbolt.Tx) error {
		rep = OAuthSealReport{}
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		rows, err := collectOAuthRows(bucket)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if shape, _ := tokenseal.Classify(row.value); shape != tokenseal.ShapePlaintext {
				continue
			}
			record, err := decodeOAuthRecord(b.oauthKey, row.key, row.value)
			if err != nil {
				// Plaintext that is not a record: undecryptable, untouched.
				// Name the key and the reason, never the value.
				if b.logger != nil {
					b.logger.Warnw("OAuth token row is not a valid record; left as plaintext",
						"server_key", row.key, "reason", OAuthSkipMalformed)
				}
				rep.Skipped = append(rep.Skipped, OAuthSkippedRow{BucketKey: row.key, Reason: OAuthSkipMalformed})
				continue
			}
			// Keep the stored JSON's unknown fields (a row a newer binary
			// wrote): sealing must not drop them (Spec 123 review).
			if err := putOAuthRecordKeeping(bucket, b.oauthKey, row.key, record, row.value); err != nil {
				if stderrors.Is(err, tokenseal.ErrPlaintextTooLarge) {
					// A per-row reason: leave this row as it is and keep
					// sealing the others. Only transaction-level failures
					// fail the sweep (and with it startup).
					if b.logger != nil {
						b.logger.Warnw("OAuth token row is too large to seal; left as plaintext",
							"server_key", row.key, "reason", OAuthSkipTooLarge, "limit_bytes", tokenseal.MaxPlaintextLen)
					}
					rep.Skipped = append(rep.Skipped, OAuthSkippedRow{BucketKey: row.key, Reason: OAuthSkipTooLarge})
					continue
				}
				return fmt.Errorf("seal oauth token %s: %w", row.key, err)
			}
			rep.Sealed++
		}
		if dryRun {
			return errOAuthDryRun
		}
		return nil
	})
	if dryRun && stderrors.Is(err, errOAuthDryRun) {
		return rep, nil
	}
	if err != nil {
		return OAuthSealReport{}, err
	}
	return rep, nil
}

// DecryptOAuthTokens rewrites every sealed row the configured key opens as
// plaintext JSON, in ONE transaction that collects every row first and writes
// after the walk (Spec 123 FR-032, the downgrade and disable path). Rows the
// key cannot open, and plaintext rows, are left exactly as they are. It needs
// the key: without one it is an error. It writes through putOAuthRecord with a
// nil key, the only plaintext writer while a key is configured; the
// plaintext it writes lands in live pages and the ciphertext it replaces in
// freed ones, so it does not touch the compaction-advised marker (a later
// seal of these rows sets it, and that is what makes the page holding this
// plaintext a reason to compact). Any write error rolls everything back.
func (b *BoltDB) DecryptOAuthTokens(ctx context.Context) (OAuthDecryptReport, error) {
	var rep OAuthDecryptReport
	if b.oauthKey == nil {
		return rep, fmt.Errorf("no OAuth token encryption key configured: nothing can be decrypted")
	}
	err := b.db.Update(func(tx *bbolt.Tx) error {
		rep = OAuthDecryptReport{}
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		rows, err := collectOAuthRows(bucket)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if shape, _ := tokenseal.Classify(row.value); shape != tokenseal.ShapeSealedV1 {
				continue
			}
			plain, err := b.oauthKey.Open(OAuthTokenBucket, row.key, row.value)
			if err != nil {
				continue // undecryptable: left alone
			}
			record, err := decodeOAuthRecord(b.oauthKey, row.key, row.value)
			if err != nil {
				continue
			}
			if err := putOAuthRecordKeeping(bucket, nil, row.key, record, plain); err != nil {
				return fmt.Errorf("decrypt oauth token %s: %w", row.key, err)
			}
			rep.Decrypted++
		}
		return nil
	})
	if err != nil {
		return OAuthDecryptReport{}, err
	}
	return rep, nil
}

// OAuthCompactAdvised reports whether the compaction-advised marker is set
// (Spec 123 FR-016): some write replaced a plaintext oauth_tokens value with
// an envelope, so freed pages may still hold the old plaintext.
func (b *BoltDB) OAuthCompactAdvised() (bool, error) {
	advised := false
	err := b.db.View(func(tx *bbolt.Tx) error {
		meta := tx.Bucket([]byte(MetaBucket))
		advised = meta != nil && meta.Get([]byte(OAuthSealCompactAdvisedKey)) != nil
		return nil
	})
	return advised, err
}
