//go:build !server

package main

import "github.com/spf13/cobra"

// registerServerEditionCommands is a no-op in the personal edition.
func registerServerEditionCommands(_ *cobra.Command) {}
