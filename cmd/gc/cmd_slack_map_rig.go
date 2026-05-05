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
		workspaceID    string
		channels       []string
		remove         bool
		removeChannels []string
		slingTarget    string
		fixFormula     string
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

--remove drops the entire rig record. --remove-channels drops just
the listed channels from the rig's set; if the resulting set is
empty, the record itself is deleted. Both are idempotent — a missing
record (or unknown channel) is a silent no-op. --remove,
--remove-channels, and --channel are mutually exclusive.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slingTargetSet := cmd.Flags().Changed("sling-target")
			fixFormulaSet := cmd.Flags().Changed("fix-formula")
			return runSlackMapRig(stdout, stderr, args[0], workspaceID, channels, remove, removeChannels,
				slingTarget, slingTargetSet, fixFormula, fixFormulaSet)
		},
	}
	defaultWorkspace := slackWorkspaceIDDefault()
	cmd.Flags().StringVar(&workspaceID, "workspace-id", defaultWorkspace,
		slackWorkspaceIDFlagUsage)
	cmd.Flags().StringSliceVar(&channels, "channel", nil,
		"Slack channel id to include in the rig's set; repeat or comma-separate for multiple")
	cmd.Flags().BoolVar(&remove, "remove", false,
		"Remove the rig record entirely (idempotent; mutually exclusive with --channel and --remove-channels)")
	cmd.Flags().StringSliceVar(&removeChannels, "remove-channels", nil,
		"Drop these channels from the rig's set; if the set becomes empty the record is deleted (idempotent; mutually exclusive with --channel and --remove)")
	cmd.Flags().StringVar(&slingTarget, "sling-target", "",
		"Qualified agent name (`<rig>/<role>`, e.g. `mission-control/polecat`) the adapter targets when dispatching for this rig. Stored on the rig record; required at use time. Omit on re-bind to preserve the current value.")
	cmd.Flags().StringVar(&fixFormula, "fix-formula", "",
		"Molecule name spawned by the adapter when this rig handles an inbound interaction (e.g. `mol-slack-fix-issue`). Optional. Omit on re-bind to preserve the current value.")
	if defaultWorkspace == "" {
		_ = cmd.MarkFlagRequired("workspace-id")
	}
	// Three-way mutual exclusion: --remove drops the whole record;
	// --remove-channels drops a subset; --channel adds/replaces.
	// Cobra surfaces these as parse-time errors with standardized
	// text, matching cmd_slack_map_channel.go's --rig/--session pair.
	cmd.MarkFlagsMutuallyExclusive("remove", "remove-channels")
	cmd.MarkFlagsMutuallyExclusive("remove", "channel")
	cmd.MarkFlagsMutuallyExclusive("remove-channels", "channel")
	return cmd
}

func runSlackMapRig(stdout, stderr io.Writer, rigName, workspaceID string, channels []string, remove bool, removeChannels []string,
	slingTarget string, slingTargetSet bool, fixFormula string, fixFormulaSet bool) error {
	cityPath, err := resolveCity()
	if err != nil {
		return fmt.Errorf("resolve city: %w", err)
	}

	// Validate --sling-target shape early — fail before touching disk.
	if slingTargetSet && slingTarget != "" {
		if err := validateSlingTarget(slingTarget); err != nil {
			return fmt.Errorf("--sling-target: %w", err)
		}
	}

	if remove {
		// Mutual exclusion enforced at parse time by cobra
		// MarkFlagsMutuallyExclusive (see newSlackMapRigCmd).
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

	if len(removeChannels) > 0 {
		return runSlackMapRigRemoveChannels(stdout, cityPath, rigName, workspaceID, removeChannels)
	}

	if len(channels) == 0 {
		return fmt.Errorf("--channel is required (one or more) unless --remove or --remove-channels is set")
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

	// Cross-store conflict check is best-effort: load registry A, then write registry B.
	// CLI invocations are not atomic across both stores. Concurrent invocations between
	// check and write may produce overlap; the adapter detects and surfaces this via
	// `gc slack status` (conflict annotation) and a startup WARN log.
	//
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
			return fmt.Errorf("map-rig: channel %q is already bound to rig %q via 'gc slack map-channel'; remove that binding first or pick a different channel set",
				ch, rec.TargetID)
		}
	}

	// Stderr WARN on dropped channels: a re-bind that omits a
	// previously-bound channel is almost always operator surprise
	// (forgot to include it). Include the dropped set so they can
	// recover with one re-run.
	//
	// Preserve sling_target / fix_formula on idempotent re-bind when
	// the operator did not supply the corresponding flag — mirrors the
	// CreatedAt-preservation behavior so that re-binding a channel set
	// doesn't silently clear dispatch routing.
	effectiveSlingTarget := slingTarget
	effectiveFixFormula := fixFormula
	if existing, ok := rigReg.Get(workspaceID, rigName); ok {
		dropped := diffStrings(existing.ChannelIDs, desired)
		if len(dropped) > 0 {
			fmt.Fprintf(stderr, "Rig %q had channels %v; replacing with %v (dropped: %s). To preserve channels across re-bind, include them all in --channel.\n", //nolint:errcheck
				rigName, existing.ChannelIDs, desired, strings.Join(dropped, ", "))
		}
		if !slingTargetSet {
			effectiveSlingTarget = existing.SlingTarget
		}
		if !fixFormulaSet {
			effectiveFixFormula = existing.FixFormula
		}
	}

	now := time.Now().UTC()
	rec := slackRigMappingRecord{
		WorkspaceID: workspaceID,
		RigName:     rigName,
		ChannelIDs:  desired,
		SlingTarget: effectiveSlingTarget,
		FixFormula:  effectiveFixFormula,
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

// runSlackMapRigRemoveChannels handles `gc slack map-rig <rig>
// --remove-channels c1,c2`. It loads the existing rig record, drops
// the requested channels from the set, and either re-Sets the
// remaining channels or Removes the record entirely if the set
// becomes empty. Idempotent: a missing rig or channels not present in
// the record are silent no-ops.
func runSlackMapRigRemoveChannels(stdout io.Writer, cityPath, rigName, workspaceID string, removeChannels []string) error {
	toRemove := dedupSortedChannels(removeChannels)
	if len(toRemove) == 0 {
		return fmt.Errorf("--remove-channels must include at least one non-empty value")
	}

	reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityPath))
	if err != nil {
		return fmt.Errorf("open slack rig mapping registry: %w", err)
	}

	existing, ok := reg.Get(workspaceID, rigName)
	if !ok {
		fmt.Fprintf(stdout, "No rig mapping %s (workspace=%s); nothing to remove channels from\n", rigName, workspaceID) //nolint:errcheck
		fmt.Fprintln(stdout, slackMapRigRestartReminder)                                                                 //nolint:errcheck
		return nil
	}

	remaining := diffStrings(existing.ChannelIDs, toRemove)
	actuallyRemoved := diffStrings(existing.ChannelIDs, remaining)

	if len(remaining) == 0 {
		existed, err := reg.Remove(workspaceID, rigName)
		if err != nil {
			return fmt.Errorf("remove slack rig mapping for %q: %w", rigName, err)
		}
		if existed {
			fmt.Fprintf(stdout, "Removed rig mapping %s (workspace=%s) — channel set became empty after removing %v\n", //nolint:errcheck
				rigName, workspaceID, actuallyRemoved)
		}
		fmt.Fprintln(stdout, slackMapRigRestartReminder) //nolint:errcheck
		return nil
	}

	if len(actuallyRemoved) == 0 {
		fmt.Fprintf(stdout, "Rig %s (workspace=%s): no channels matched %v; channel set unchanged %v\n", //nolint:errcheck
			rigName, workspaceID, toRemove, existing.ChannelIDs)
		fmt.Fprintln(stdout, slackMapRigRestartReminder) //nolint:errcheck
		return nil
	}

	updated := slackRigMappingRecord{
		WorkspaceID: workspaceID,
		RigName:     rigName,
		ChannelIDs:  remaining,
		// Preserve sling_target / fix_formula across partial channel
		// removal — operators removing a channel don't expect dispatch
		// routing to be silently cleared.
		SlingTarget: existing.SlingTarget,
		FixFormula:  existing.FixFormula,
		// CreatedAt is preserved by Set when the record exists.
		CreatedAt: existing.CreatedAt,
		UpdatedAt: time.Now().UTC(),
	}
	if err := reg.Set(updated); err != nil {
		return fmt.Errorf("persist slack rig mapping after partial removal: %w", err)
	}
	fmt.Fprintf(stdout, "Rig %s (workspace=%s): removed channels %v; remaining %v\n", //nolint:errcheck
		rigName, workspaceID, actuallyRemoved, remaining)
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
