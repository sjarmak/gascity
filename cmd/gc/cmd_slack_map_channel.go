package main

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

// newSlackMapChannelCmd returns `gc slack map-channel` — the verb that
// persists a (workspace_id, channel_id) → (rig|session) binding (or
// removes one) at <cityPath>/.gc/slack/channel_mappings.json. The
// slack-pack adapter reads this file at startup and uses it to route
// /slack/interactions slash-command requests.
func newSlackMapChannelCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		workspaceID string
		rigName     string
		sessionID   string
		remove      bool
	)
	cmd := &cobra.Command{
		Use:   "map-channel <channel-id>",
		Short: "Bind a Slack channel to a gc rig or session for slash-command routing",
		Long: `Bind a Slack channel to a gc rig or session for slash-command routing.

Persists a (workspace_id, channel_id) → (target_kind, target_id) record
at <cityPath>/.gc/slack/channel_mappings.json. The slack-pack adapter
reads this file at startup and routes incoming /slack/interactions
slash-command requests for the channel to the bound target.

Exactly one of --rig or --session is required (unless --remove). The
binding is idempotent: re-binding the same channel preserves the
original CreatedAt and overwrites the target fields. --remove always
exits 0 — if no binding exists, the command is a no-op.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSlackMapChannel(stdout, args[0], workspaceID, rigName, sessionID, remove)
		},
	}
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "",
		"Slack workspace (team) id, e.g. T0123456 (required)")
	cmd.Flags().StringVar(&rigName, "rig", "",
		"Bind the channel to a gc rig (mutually exclusive with --session)")
	cmd.Flags().StringVar(&sessionID, "session", "",
		"Bind the channel to a gc session (mutually exclusive with --rig)")
	cmd.Flags().BoolVar(&remove, "remove", false,
		"Remove the binding for <channel-id> if one exists (idempotent)")
	_ = cmd.MarkFlagRequired("workspace-id")
	cmd.MarkFlagsMutuallyExclusive("rig", "session")
	return cmd
}

func runSlackMapChannel(stdout io.Writer, channelID, workspaceID, rigName, sessionID string, remove bool) error {
	cityPath, err := resolveCity()
	if err != nil {
		return fmt.Errorf("resolve city: %w", err)
	}
	if remove {
		if rigName != "" || sessionID != "" {
			return fmt.Errorf("--remove cannot be combined with --rig or --session")
		}
		reg, err := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityPath))
		if err != nil {
			return fmt.Errorf("open slack channel mapping registry: %w", err)
		}
		existed, err := reg.Remove(workspaceID, channelID)
		if err != nil {
			return fmt.Errorf("remove slack channel mapping for %q: %w", channelID, err)
		}
		if existed {
			fmt.Fprintf(stdout, "Removed channel mapping %s (workspace=%s)\n", channelID, workspaceID) //nolint:errcheck
		} else {
			fmt.Fprintf(stdout, "No binding for channel %s (workspace=%s); nothing to remove\n", channelID, workspaceID) //nolint:errcheck
		}
		return nil
	}

	if rigName == "" && sessionID == "" {
		return fmt.Errorf("exactly one of --rig or --session is required (or use --remove)")
	}

	var (
		targetKind string
		targetID   string
	)
	switch {
	case rigName != "":
		targetKind = slackChannelMappingTargetKindRig
		targetID = rigName
	default:
		targetKind = slackChannelMappingTargetKindSession
		targetID = sessionID
	}

	now := time.Now().UTC()
	rec := slackChannelMappingRecord{
		WorkspaceID: workspaceID,
		ChannelID:   channelID,
		TargetKind:  targetKind,
		TargetID:    targetID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	reg, err := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityPath))
	if err != nil {
		return fmt.Errorf("open slack channel mapping registry: %w", err)
	}
	if err := reg.Set(rec); err != nil {
		return fmt.Errorf("persist slack channel mapping: %w", err)
	}
	fmt.Fprintf(stdout, "Mapped channel %s (workspace=%s) → %s:%s\n", //nolint:errcheck
		channelID, workspaceID, targetKind, targetID)
	return nil
}
