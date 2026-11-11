package storage

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"

// Option configures how NewBoltDB and NewManager open the database.
type Option func(*openOptions)

type openOptions struct {
	oauthKey *tokenseal.Key
}

// WithOAuthTokenKey sets the key that seals and opens oauth_tokens values
// (Spec 123). A nil key means encryption is off: rows are written as plain
// JSON, and sealed rows read as undecryptable. Every production open site
// passes this option (TestStorageOpenSitesPassOAuthKey), so no process that
// opens config.db can write plaintext while the daemon would seal.
func WithOAuthTokenKey(k *tokenseal.Key) Option {
	return func(o *openOptions) { o.oauthKey = k }
}

func resolveOpenOptions(opts []Option) openOptions {
	var o openOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}
