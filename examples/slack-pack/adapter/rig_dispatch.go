package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
)

// dispatchExecCommand is the indirection point for `bd` and `gc`
// subprocess invocation, used by the rig-target dispatch path
// (gc-cby.18.3). Tests override this var to install a fake command
// runner that records invocations and returns canned output. Production
// uses exec.Command directly.
var dispatchExecCommand = exec.Command

// dispatchTestCompletionHook fires once at the end of every rig
// dispatch goroutine — happy path or error path. Tests install this
// hook to synchronize on dispatch completion without polling. Nil in
// production. Not exported (test-only).
var dispatchTestCompletionHook func()

// rigDispatchTitleMaxLen caps the bead title sourced from slash-command
// text or block-action value. Slack inputs can be arbitrarily long and
// `bd` titles flow into convoy summaries, log lines, and dashboard
// rows; cap them well below screen-line widths. The remaining text is
// preserved verbatim in the agent's view via the dispatch goroutine's
// system-reminder body in a follow-up modal capture (cby.18.4).
const rigDispatchTitleMaxLen = 200

// truncateForTitle returns s capped at rigDispatchTitleMaxLen runes,
// using a fall-back placeholder when s trims to empty. It runs after
// neutralizeMarkupBoundaries so callers don't lose sanitization.
func truncateForTitle(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return "(empty)"
	}
	if len(t) > rigDispatchTitleMaxLen {
		return t[:rigDispatchTitleMaxLen]
	}
	return t
}

// runDispatchTestHook is internal; the rig dispatch goroutine calls it
// at the end of every code path so tests can synchronize on completion.
func runDispatchTestHook() {
	if dispatchTestCompletionHook != nil {
		dispatchTestCompletionHook()
	}
}

// dispatchSlashCommandToRig is the rig-target counterpart of
// dispatchSlashCommandToSession. It runs the synchronous validation
// (sling-target lookup, rig workdir resolution, dispatch-slot acquire)
// and writes the appropriate ephemeral; on success it spawns the
// background dispatch goroutine that calls `bd create` then `gc sling`.
//
// The function is invoked from handleSlackInteractions's rig branch and
// owns the HTTP response — every code path here either calls
// writeEphemeral or writes a synchronous ack and returns.
func dispatchSlashCommandToRig(
	w http.ResponseWriter,
	cfg config,
	rigReg *rigMappingRegistry,
	workspaceID, rigName, command, text, channelID, userID string,
) {
	if rigReg == nil {
		writeEphemeral(w, http.StatusOK, fmt.Sprintf(
			"channel is bound to rig %q but no rig registry is loaded; ensure SLACK_RIG_MAPPING_PATH points at a readable rig_mappings.json and restart the adapter",
			rigName))
		return
	}
	target, fixFormula, err := rigReg.ResolveSlingTarget(workspaceID, rigName)
	if err != nil {
		// ResolveSlingTarget errors are operator-actionable fix-its;
		// surface verbatim.
		writeEphemeral(w, http.StatusOK, err.Error())
		return
	}
	workdir, err := rigWorkdir(cfg.cityPath, rigName)
	if err != nil {
		writeEphemeral(w, http.StatusOK, fmt.Sprintf(
			"rig workdir not found in routes.jsonl: %v", err))
		return
	}

	release, capacity, acquired := acquireDispatchSlot()
	if !acquired {
		log.Printf("slack adapter: dispatch queue full (cap=%d), dropping slash command=%q channel=%q rig=%q",
			capacity, command, channelID, rigName)
		writeEphemeral(w, http.StatusOK,
			"Slack adapter is currently saturated; please retry shortly.")
		return
	}

	writeEphemeral(w, http.StatusOK, fmt.Sprintf(
		"Routing %s to rig %s…", command, rigName))

	title := fmt.Sprintf("[slack/%s by %s] %s",
		neutralizeMarkupBoundaries(channelID),
		neutralizeMarkupBoundaries(userID),
		neutralizeMarkupBoundaries(text),
	)
	title = truncateForTitle(title)

	go func() {
		defer release()
		defer runDispatchTestHook()
		runRigDispatch(workdir, cfg.cityPath, target, fixFormula, title, rigName)
	}()
}

// dispatchBlockActionsToRig is the rig-target counterpart of
// dispatchBlockActionsToSession. The bead title carries the first
// action's identifier and value so an agent picking up the bead has
// enough context to decide what to do. Multi-action payloads are not
// flattened into the title — Slack typically sends length 1, and
// multi_*_select bursts are best surfaced via the modal capture path
// (cby.18.4).
func dispatchBlockActionsToRig(
	w http.ResponseWriter,
	cfg config,
	rigReg *rigMappingRegistry,
	workspaceID, rigName, channelID string,
	p *slackInteractionPayload,
) {
	if rigReg == nil {
		writeEphemeral(w, http.StatusOK, fmt.Sprintf(
			"channel is bound to rig %q but no rig registry is loaded; ensure SLACK_RIG_MAPPING_PATH points at a readable rig_mappings.json and restart the adapter",
			rigName))
		return
	}
	target, fixFormula, err := rigReg.ResolveSlingTarget(workspaceID, rigName)
	if err != nil {
		writeEphemeral(w, http.StatusOK, err.Error())
		return
	}
	workdir, err := rigWorkdir(cfg.cityPath, rigName)
	if err != nil {
		writeEphemeral(w, http.StatusOK, fmt.Sprintf(
			"rig workdir not found in routes.jsonl: %v", err))
		return
	}

	release, capacity, acquired := acquireDispatchSlot()
	if !acquired {
		log.Printf("slack adapter: dispatch queue full (cap=%d), dropping block_actions team=%q channel=%q rig=%q",
			capacity, workspaceID, channelID, rigName)
		writeEphemeral(w, http.StatusOK,
			"Slack adapter is currently saturated; please retry shortly.")
		return
	}

	writeEphemeral(w, http.StatusOK, fmt.Sprintf(
		"Routing block-action to rig %s…", rigName))

	var actionID, actionValue string
	if len(p.Actions) > 0 {
		actionID = p.Actions[0].ActionID
		actionValue = p.Actions[0].Value
		if actionValue == "" && p.Actions[0].SelectedOption != nil {
			actionValue = p.Actions[0].SelectedOption.Value
		}
	}
	title := fmt.Sprintf("[slack/%s block_actions %s by %s] %s",
		neutralizeMarkupBoundaries(channelID),
		neutralizeMarkupBoundaries(actionID),
		neutralizeMarkupBoundaries(p.User.ID),
		neutralizeMarkupBoundaries(actionValue),
	)
	title = truncateForTitle(title)

	go func() {
		defer release()
		defer runDispatchTestHook()
		runRigDispatch(workdir, cfg.cityPath, target, fixFormula, title, rigName)
	}()
}

// runRigDispatch performs the two subprocess legs of a rig-target
// dispatch: `bd create` inside the rig workdir to mint a task bead,
// then `gc sling <target> <bead_id> [--on <fix_formula>]` from the
// city root to invoke dispatch. Failures at the gc-sling leg trigger a
// best-effort `bd close <bead_id> -r dispatch_failed` so the orphan
// task does not show up as queued work.
//
// Empty fixFormula deliberately omits the --on flag (cby.18.3 design
// choice): gc sling falls through to its own default formula
// resolution rather than the adapter inventing one.
func runRigDispatch(workdir, cityPath, target, fixFormula, title, rigName string) {
	beadID, err := runBdCreate(workdir, title)
	if err != nil {
		log.Printf("rig dispatch: bd create in %s rig=%q: %v", workdir, rigName, err)
		return
	}
	if err := runGcSling(cityPath, target, beadID, fixFormula); err != nil {
		log.Printf("rig dispatch: gc sling target=%q bead=%s rig=%q: %v",
			target, beadID, rigName, err)
		closeOrphanBead(workdir, beadID)
		return
	}
	log.Printf("rig dispatch: bead=%s -> sling=%s formula=%q rig=%q OK",
		beadID, target, fixFormula, rigName)
}

// runBdCreate invokes `bd create --json <title> -t task` inside the
// rig workdir and returns the parsed bead id from stdout.
func runBdCreate(workdir, title string) (string, error) {
	cmd := dispatchExecCommand("bd", "create", "--json", title, "-t", "task")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("bd create exec in %q: %w", workdir, err)
	}
	var rec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &rec); err != nil {
		return "", fmt.Errorf("decode bd create output (%q): %w", string(out), err)
	}
	if rec.ID == "" {
		return "", fmt.Errorf("bd create returned empty id (output %q)", string(out))
	}
	return rec.ID, nil
}

// runGcSling invokes `gc sling <target> <beadID> [--on <fixFormula>]`
// from cityPath. Empty fixFormula omits --on so gc applies its
// configured default formula (cby.18.3).
func runGcSling(cityPath, target, beadID, fixFormula string) error {
	args := []string{"sling", target, beadID}
	if fixFormula != "" {
		args = append(args, "--on", fixFormula)
	}
	cmd := dispatchExecCommand("gc", args...)
	cmd.Dir = cityPath
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gc %s in %q: %w (output=%q)",
			strings.Join(args, " "), cityPath, err, string(out))
	}
	return nil
}

// closeOrphanBead best-effort closes a bead created by runBdCreate
// after `gc sling` failed. Errors are logged and swallowed — the bead
// is already orphaned at this point and a closure failure is at most a
// queued-work display nit, not a correctness issue.
func closeOrphanBead(workdir, beadID string) {
	cmd := dispatchExecCommand("bd", "close", beadID, "-r", "dispatch_failed")
	cmd.Dir = workdir
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("rig dispatch: bd close %s in %q: %v (output=%q)",
			beadID, workdir, err, string(out))
		return
	}
	log.Printf("rig dispatch: closed orphan bead=%s with reason=dispatch_failed", beadID)
}
