//go:build server

package oauth

// editionServerFieldMaskDecisions records the write-path answer for the
// server-edition-only `auth_broker` block (config.AuthBrokerConfig).
//
// Every leaf is REFUSED. The block is replaced wholesale by a write and nothing
// binds a mask back into it, so an echo of a read mask must be rejected rather
// than persisted over the brokered credential — the same answer `args` and
// `oauth.scopes` get, and the same reason.
var editionServerFieldMaskDecisions = map[string]MaskDecision{
	"auth_broker.mode":                   MaskDecisionRefuse,
	"auth_broker.token_endpoint":         MaskDecisionRefuse,
	"auth_broker.authorization_endpoint": MaskDecisionRefuse,
	"auth_broker.resource":               MaskDecisionRefuse,
	"auth_broker.scopes":                 MaskDecisionRefuse,
	"auth_broker.client_id":              MaskDecisionRefuse,
	"auth_broker.client_secret":          MaskDecisionRefuse,
	"auth_broker.header":                 MaskDecisionRefuse,
	"auth_broker.header_format":          MaskDecisionRefuse,
}
