//go:build !server

package oauth

// editionServerFieldMaskDecisions is empty in the personal edition:
// config.ServerConfig.AuthBroker is a stub struct with no fields on the wire,
// so there is nothing under `auth_broker` to decide about.
var editionServerFieldMaskDecisions = map[string]MaskDecision{}
