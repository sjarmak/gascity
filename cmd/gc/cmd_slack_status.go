package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// slackStatusJSON is the wire shape emitted by `gc slack status --json`.
// Both arrays are non-nil even on empty stores so consumers can rely on
// the keys being present.
type slackStatusJSON struct {
	Apps     []slackAppRecord            `json:"apps"`
	Mappings []slackChannelMappingRecord `json:"channel_mappings"`
}

// newSlackStatusCmd returns `gc slack status` — the unified observability
// verb across the slack-pack registries (apps + channel mappings).
func newSlackStatusCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		channelFilter   string
		workspaceFilter string
		jsonOutput      bool
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show imported Slack apps and channel mappings for the current city",
		Long: `Show imported Slack apps and channel mappings for the current city.

Reads <cityPath>/.gc/slack/apps.json (written by 'gc slack import-app')
and <cityPath>/.gc/slack/channel_mappings.json (written by
'gc slack map-channel') and prints a unified summary.

--channel <id> filters channel mappings to the named channel.
--workspace-id <id> filters both sections to the named workspace.
--json emits a machine-readable shape with top-level keys "apps" and
"channel_mappings".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSlackStatus(stdout, channelFilter, workspaceFilter, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&channelFilter, "channel", "",
		"Filter channel mappings to a single channel id")
	cmd.Flags().StringVar(&workspaceFilter, "workspace-id", "",
		"Filter both apps and mappings to a single Slack workspace id")
	cmd.Flags().BoolVar(&jsonOutput, "json", false,
		"Emit machine-readable JSON instead of human-readable text")
	return cmd
}

func runSlackStatus(stdout io.Writer, channelFilter, workspaceFilter string, jsonOutput bool) error {
	cityPath, err := resolveCity()
	if err != nil {
		return fmt.Errorf("resolve city: %w", err)
	}

	appReg, err := newSlackAppRegistry(slackAppsRegistryPath(cityPath))
	if err != nil {
		return fmt.Errorf("open slack app registry: %w", err)
	}
	mapReg, err := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityPath))
	if err != nil {
		return fmt.Errorf("open slack channel mapping registry: %w", err)
	}

	apps := make([]slackAppRecord, 0)
	for _, a := range appReg.All() {
		if workspaceFilter != "" && a.WorkspaceID != workspaceFilter {
			continue
		}
		apps = append(apps, a)
	}
	mappings := make([]slackChannelMappingRecord, 0)
	for _, m := range mapReg.All() {
		if workspaceFilter != "" && m.WorkspaceID != workspaceFilter {
			continue
		}
		if channelFilter != "" && m.ChannelID != channelFilter {
			continue
		}
		mappings = append(mappings, m)
	}

	if jsonOutput {
		out := slackStatusJSON{Apps: apps, Mappings: mappings}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	return printSlackStatusHuman(stdout, apps, mappings)
}

func printSlackStatusHuman(stdout io.Writer, apps []slackAppRecord, mappings []slackChannelMappingRecord) error {
	if len(apps) == 0 {
		fmt.Fprintln(stdout, "No slack apps imported.") //nolint:errcheck
	} else {
		fmt.Fprintf(stdout, "Slack apps (%d):\n", len(apps)) //nolint:errcheck
		for _, a := range apps {
			fmt.Fprintf(stdout, "  %s/%s: %s (scopes=%d, slash_commands=%d)\n", //nolint:errcheck
				a.WorkspaceID, a.AppID, a.DisplayName, len(a.Scopes), len(a.SlashCommands))
		}
	}
	if len(mappings) == 0 {
		fmt.Fprintln(stdout, "No channel mappings.") //nolint:errcheck
	} else {
		fmt.Fprintf(stdout, "Channel mappings (%d):\n", len(mappings)) //nolint:errcheck
		for _, m := range mappings {
			fmt.Fprintf(stdout, "  %s/%s -> %s:%s\n", //nolint:errcheck
				m.WorkspaceID, m.ChannelID, m.TargetKind, m.TargetID)
		}
	}
	return nil
}
