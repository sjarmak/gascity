package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// slackMapRigRestartReminder is the trailing line printed on every
// success path of `gc slack map-rig` AND `gc slack map-channel` so
// operators are reminded that the slack-pack adapter loads the
// registries once at startup. SIGHUP reload is a follow-up bead.
const slackMapRigRestartReminder = "Restart slack-pack adapter (gc service restart slack) to pick up the binding."

// newSlackMapRigCmd returns `gc slack map-rig` — the verb that
// persists a (workspace_id, rig_name) → set-of-channel-ids binding
// (or removes one) at <cityPath>/.gc/slack/rig_mappings.json. The
// slack-pack adapter reads this file at startup and uses it as the
// fall-through default when no per-channel `map-channel` binding
// exists.
func newSlackMapRigCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		workspaceID string
		channels    []string
		remove      bool
	)
	cmd := &cobra.Command{
		Use:   "map-rig <rig-name>",
		Short: "Bind a Slack rig to a set of channels for slash-command default routing",
		Long: `Bind a Slack rig to a set of channels for slash-command default routing.

Persists a (workspace_id, rig_name) → set-of-channel-ids record at
<cityPath>/.gc/slack/rig_mappings.json. The slack-pack adapter reads
this file at startup and uses it as the fall-through resolver when
no per-channel 'map-channel' binding exists for an inbound channel.

The binding is idempotent: re-binding the same rig replaces the
channel set (sorted+deduped) and preserves the original CreatedAt.
Channels can be supplied as repeated --channel flags, comma-
separated values, or a mix.

--remove drops the entire rig record (partial channel removal is a
follow-up bead). Always exits 0 — a missing record is a no-op.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSlackMapRig(stdout, stderr, args[0], workspaceID, channels, remove)
		},
	}
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "",
		"Slack workspace (team) id, e.g. T0123456 (required)")
	cmd.Flags().StringSliceVar(&channels, "channel", nil,
		"Slack channel id to include in the rig's set; repeat or comma-separate for multiple")
	cmd.Flags().BoolVar(&remove, "remove", false,
		"Remove the rig record entirely (idempotent; mutually exclusive with --channel)")
	_ = cmd.MarkFlagRequired("workspace-id")
	return cmd
}

func runSlackMapRig(stdout, stderr io.Writer, rigName, workspaceID string, channels []string, remove bool) error {
	cityPath, err := resolveCity()
	if err != nil {
		return fmt.Errorf("resolve city: %w", err)
	}

	if remove {
		if len(channels) > 0 {
			return fmt.Errorf("--remove cannot be combined with --channel; use --remove alone to drop the rig record (partial channel removal is a follow-up bead)")
		}
		reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityPath))
		if err != nil {
			return fmt.Errorf("open slack rig mapping registry: %w", err)
		}
		existed, err := reg.Remove(workspaceID, rigName)
		if err != nil {
			return fmt.Errorf("remove slack rig mapping for %q: %w", rigName, err)
		}
		if existed {
			fmt.Fprintf(stdout, "Removed rig mapping %s (workspace=%s)\n", rigName, workspaceID) //nolint:errcheck
		} else {
			fmt.Fprintf(stdout, "No rig mapping %s (workspace=%s); nothing to remove\n", rigName, workspaceID) //nolint:errcheck
		}
		fmt.Fprintln(stdout, slackMapRigRestartReminder) //nolint:errcheck
		return nil
	}

	if len(channels) == 0 {
		return fmt.Errorf("--channel is required (one or more) unless --remove is set")
	}

	// Open both registries: cby.4 (rig) for the actual write, cby.3
	// (channel) for the cross-store conflict check.
	rigReg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityPath))
	if err != nil {
		return fmt.Errorf("open slack rig mapping registry: %w", err)
	}
	chanReg, err := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityPath))
	if err != nil {
		return fmt.Errorf("open slack channel mapping registry: %w", err)
	}

	desired := dedupSortedChannels(channels)
	if len(desired) == 0 {
		return fmt.Errorf("--channel must include at least one non-empty value")
	}

	// Cross-store conflict check (cby.3 → cby.4 direction): if any
	// channel in the desired set has a per-channel mapping pointing
	// at a DIFFERENT rig in cby.3, refuse. Same-rig and session
	// overrides are fine — the latter is the intended composition.
	for _, ch := range desired {
		rec, ok := chanReg.Get(workspaceID, ch)
		if !ok {
			continue
		}
		if rec.TargetKind == slackChannelMappingTargetKindRig && rec.TargetID != rigName {
			return fmt.Errorf("cmd/gc/cmd_slack_map_rig.go: channel %q is already bound to rig %q via 'gc slack map-channel'; remove that binding first or pick a different channel set",
				ch, rec.TargetID)
		}
	}

	// Stderr WARN on dropped channels: a re-bind that omits a
	// previously-bound channel is almost always operator surprise
	// (forgot to include it). Include the dropped set so they can
	// recover with one re-run.
	if existing, ok := rigReg.Get(workspaceID, rigName); ok {
		dropped := diffStrings(existing.ChannelIDs, desired)
		if len(dropped) > 0 {
			fmt.Fprintf(stderr, "Rig %q had channels %v; replacing with %v (dropped: %s). To preserve channels across re-bind, include them all in --channel.\n", //nolint:errcheck
				rigName, existing.ChannelIDs, desired, strings.Join(dropped, ", "))
		}
	}

	now := time.Now().UTC()
	rec := slackRigMappingRecord{
		WorkspaceID: workspaceID,
		RigName:     rigName,
		ChannelIDs:  desired,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := rigReg.Set(rec); err != nil {
		return fmt.Errorf("persist slack rig mapping: %w", err)
	}
	fmt.Fprintf(stdout, "Mapped rig %s (workspace=%s) → channels %v\n", //nolint:errcheck
		rigName, workspaceID, desired)
	fmt.Fprintln(stdout, slackMapRigRestartReminder) //nolint:errcheck
	return nil
}

// diffStrings returns the lexicographically-sorted set of elements
// in a that do not appear in b. Used to compute the dropped-channel
// set for the replace-with-drops stderr WARN.
func diffStrings(a, b []string) []string {
	in := make(map[string]struct{}, len(b))
	for _, s := range b {
		in[s] = struct{}{}
	}
	out := make([]string, 0)
	for _, s := range a {
		if _, ok := in[s]; ok {
			continue
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
