//go:build server

package connect

// browserLoginEditionSupported: a server-edition binary never offers browser login, whatever the config (Spec 132).
const browserLoginEditionSupported = false
