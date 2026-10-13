//go:build server

package server

// clientsEdition is false in the server edition: there is no per-client
// surface, so `client=` subjects answer 404.
const clientsEdition = false
