//go:build !server

package server

// clientsEdition reports whether the edition under test serves the per-client
// REST surface (personal only). Client-dependent assertions in the profile v3
// REST tests run only where it is true.
const clientsEdition = true
