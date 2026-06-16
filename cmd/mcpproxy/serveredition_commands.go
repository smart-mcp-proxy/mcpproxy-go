//go:build server

package main

import "github.com/spf13/cobra"

// registerServerEditionCommands adds CLI commands that only exist in the server
// edition. rootCmd is a local in main(), so the personal/server split is done
// via this build-tagged hook (mirrors collectServerEditionInfo).
func registerServerEditionCommands(root *cobra.Command) {
	root.AddCommand(newCredentialCommand())
}
