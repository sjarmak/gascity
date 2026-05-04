package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// slackStatusRigMapping wraps a slackRigMappingRecord with a Conflict
// flag set to true when a per-channel mapping claims a channel in
// this rig's set for a DIFFERENT rig (cby.3 channel mapping wins).
// Operators should resolve such conflicts by `gc slack map-channel
// <C> --remove` or `gc slack map-rig <name> --remove`.
type slackStatusRigMapping struct {
	Record   slackRigMappingRecord `json:",inline"`
	Conflict bool                  `json:"conflict"`
}

// MarshalJSON inlines Record's fields so the wire shape stays flat.
// Without this, the JSON would have a `Record` envelope which is
// noisier than necessary for an observability surface.
func (s slackStatusRigMapping) MarshalJSON() ([]byte, error) {
	type recAlias slackRigMappingRecord
	out := struct {
		recAlias
		Conflict bool `json:"conflict"`
	}{recAlias(s.Record), s.Conflict}
	return json.Marshal(out)
}

// UnmarshalJSON reads the flat shape so tests can round-trip the
// JSON.
func (s *slackStatusRigMapping) UnmarshalJSON(data []byte) error {
	type recAlias slackRigMappingRecord
	var in struct {
		recAlias
		Conflict bool `json:"conflict"`
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	s.Record = slackRigMappingRecord(in.recAlias)
	s.Conflict = in.Conflict
	return nil
}

// slackStatusJSON is the wire shape emitted by `gc slack status --json`.
// All arrays are non-nil even on empty stores so consumers can rely
// on the keys being present.
type slackStatusJSON struct {
	Apps        []slackAppRecord            `json:"apps"`
	Mappings    []slackChannelMappingRecord `json:"channel_mappings"`
	RigMappings []slackStatusRigMapping     `json:"rig_mappings"`
}

// newSlackStatusCmd returns `gc slack status` — the unified observability
// verb across the slack-pack registries (apps + channel mappings +
// rig mappings).
func newSlackStatusCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		channelFilter   string
		workspaceFilter string
		jsonOutput      bool
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show imported Slack apps, channel mappings, and rig mappings for the current city",
		Long: `Show imported Slack apps, channel mappings, and rig mappings for the current city.

Reads <cityPath>/.gc/slack/apps.json (written by 'gc slack import-app'),
<cityPath>/.gc/slack/channel_mappings.json (written by
'gc slack map-channel'), and <cityPath>/.gc/slack/rig_mappings.json
(written by 'gc slack map-rig') and prints a unified summary.

Per-channel mappings are overrides on top of rig→{channels} defaults;
the channel mapping wins. When the two stores claim the same channel
for DIFFERENT rigs, the rig-mapping section is annotated with
"(conflict: cby.3 channel mapping wins)" in human output and the
JSON shape sets "conflict": true.

--channel <id> filters channel mappings to the named channel AND
filters rig mappings to records whose channel set contains <id>.
--workspace-id <id> filters all three sections to the named workspace.
--json emits a machine-readable shape with top-level keys "apps",
"channel_mappings", and "rig_mappings".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSlackStatus(stdout, channelFilter, workspaceFilter, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&channelFilter, "channel", "",
		"Filter channel mappings to a single channel id; rig mappings are filtered to records whose channel_ids contain this id")
	cmd.Flags().StringVar(&workspaceFilter, "workspace-id", "",
		"Filter apps, channel mappings, and rig mappings to a single Slack workspace id")
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
	rigReg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityPath))
	if err != nil {
		return fmt.Errorf("open slack rig mapping registry: %w", err)
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
	rigMappings := make([]slackStatusRigMapping, 0)
	for _, r := range rigReg.AllSorted() {
		if workspaceFilter != "" && r.WorkspaceID != workspaceFilter {
			continue
		}
		if channelFilter != "" && !stringsContains(r.ChannelIDs, channelFilter) {
			continue
		}
		rigMappings = append(rigMappings, slackStatusRigMapping{
			Record:   r,
			Conflict: rigConflictsWithChannelStore(r, mapReg),
		})
	}

	if jsonOutput {
		out := slackStatusJSON{Apps: apps, Mappings: mappings, RigMappings: rigMappings}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	return printSlackStatusHuman(stdout, apps, mappings, rigMappings)
}

// rigConflictsWithChannelStore returns true if any channel in rec's
// set has a per-channel mapping with target_kind="rig" pointing at a
// DIFFERENT rig. Session-target overrides do NOT count as conflicts:
// they are the documented composition pattern.
func rigConflictsWithChannelStore(rec slackRigMappingRecord, chanReg *slackChannelMappingRegistry) bool {
	for _, ch := range rec.ChannelIDs {
		if existing, ok := chanReg.Get(rec.WorkspaceID, ch); ok {
			if existing.TargetKind == slackChannelMappingTargetKindRig && existing.TargetID != rec.RigName {
				return true
			}
		}
	}
	return false
}

func printSlackStatusHuman(stdout io.Writer, apps []slackAppRecord, mappings []slackChannelMappingRecord, rigMappings []slackStatusRigMapping) error {
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
	if len(rigMappings) == 0 {
		fmt.Fprintln(stdout, "No rig mappings.") //nolint:errcheck
	} else {
		fmt.Fprintf(stdout, "Rig mappings (%d):\n", len(rigMappings)) //nolint:errcheck
		for _, r := range rigMappings {
			conflict := ""
			if r.Conflict {
				conflict = " (conflict: cby.3 channel mapping wins)"
			}
			fmt.Fprintf(stdout, "  %s/%s -> channels %v%s\n", //nolint:errcheck
				r.Record.WorkspaceID, r.Record.RigName, r.Record.ChannelIDs, conflict)
		}
	}
	return nil
}
