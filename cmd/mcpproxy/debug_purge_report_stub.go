//go:build !qa_hooks

package main

import "github.com/spf13/cobra"

// registerQADebugCommands is a no-op outside the qa_hooks build (Spec 116
// T058): the purge-report inspection command never ships.
func registerQADebugCommands(_ *cobra.Command) {}
