package main

import (
	"io"

	"github.com/spf13/cobra"
)

func newSlackCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slack",
		Short: "Manage the Slack pack — apps, channel mappings, and dispatch",
		Long: `Manage the Slack pack used for human ↔ Gas City coordination.

On-disk state written by these commands lives under <cityPath>/.gc/slack/.
The slack-pack adapter (examples/slack-pack/adapter/) reads that state
via env vars injected at "gc start".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newSlackImportAppCmd(stdout, stderr))
	cmd.AddCommand(newSlackSyncCommandsCmd(stdout, stderr))
	cmd.AddCommand(newSlackMapChannelCmd(stdout, stderr))
	cmd.AddCommand(newSlackStatusCmd(stdout, stderr))
	return cmd
}
