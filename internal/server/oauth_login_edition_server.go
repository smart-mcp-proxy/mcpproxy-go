//go:build server

package server

// oauthLoginEditionSupported: a server-edition binary never serves browser login, whatever the config (Spec 132).
const oauthLoginEditionSupported = false
