package temporalmaintenance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// CommandRunner executes one proposed mutation for real. RealAdapter calls it at
// most once per idempotency key; the runner itself is free of dedup concerns.
type CommandRunner interface {
	// Run performs the mutation and returns a short result reference (a created
	// bead id, "merged", ...). It must not assume it will be retried on error —
	// RealAdapter records a failure as terminal. ctx bounds the subprocess.
	Run(ctx context.Context, m ProposedMutation) (resultRef string, err error)
}

// beadIDRe extracts the first gc-bead id from `gc bd create` output.
var beadIDRe = regexp.MustCompile(`gc-[a-z0-9]+`)

// prNumberRe validates a GitHub PR/issue number target: digits only, so a target
// can never be an option-shaped token (e.g. "--mirror") appended to a command.
var prNumberRe = regexp.MustCompile(`^[0-9]+$`)

// defaultAllow is the fixed set of binaries the maintenance runner may exec. It
// is a policy guard, not a heuristic: only these commands appear in the
// maintenance vocabulary (bead ops, sling, GitHub, git).
var defaultAllow = map[string]bool{
	"gc": true, "gc-sling": true, "gh": true, "git": true, "bd": true,
}

// gatedAction describes an approved external action: how to build its argv from a
// validated target, and what a valid target looks like. The runner — not the
// caller — is the sole authority on the argv, so a free-form Action or Argv can
// never smuggle an unapproved subcommand ("gh repo delete", "git -c alias=!id")
// or an option-shaped target ("--mirror").
type gatedAction struct {
	build       func(target string) []string
	validTarget func(target string) bool
}

// defaultGatedActions is the approved gated-action vocabulary. Extend it
// deliberately as new maintenance actions are needed — never by widening the
// binary allowlist alone.
func defaultGatedActions() map[string]gatedAction {
	prTarget := prNumberRe.MatchString
	return map[string]gatedAction{
		"gh pr merge":   {build: func(t string) []string { return []string{"gh", "pr", "merge", t} }, validTarget: prTarget},
		"gh pr review":  {build: func(t string) []string { return []string{"gh", "pr", "review", t} }, validTarget: prTarget},
		"gh pr comment": {build: func(t string) []string { return []string{"gh", "pr", "comment", t} }, validTarget: prTarget},
	}
}

// ExecRunner is the real CommandRunner: it shells out to allowlisted binaries.
// It performs no judgment — which PR/issue to act on and the polecat prompt are
// decided upstream and arrive as an approved action + a validated target + a
// body-file path.
type ExecRunner struct {
	// Allow overrides the binary allowlist (nil = defaultAllow).
	Allow map[string]bool
	// Gated overrides the approved gated-action vocabulary (nil = defaults).
	Gated map[string]gatedAction
	// Dir is the working directory for every command (e.g. /home/ds/gas-city).
	Dir string
	// Rig is the beads rig for selection bead creation (e.g. "gascity").
	Rig string
	// ScratchRoot, when set, confines a selection BodyFile to this directory
	// tree — defence against a future caller pointing --body-file at a secret.
	ScratchRoot string
}

// NewExecRunner returns a runner with the default allowlist and the gascity rig.
func NewExecRunner(dir string) *ExecRunner {
	return &ExecRunner{Allow: defaultAllow, Dir: dir, Rig: "gascity"}
}

func (r *ExecRunner) allowed(bin string) bool {
	allow := r.Allow
	if allow == nil {
		allow = defaultAllow
	}
	return allow[bin]
}

func (r *ExecRunner) gated() map[string]gatedAction {
	if r.Gated == nil {
		return defaultGatedActions()
	}
	return r.Gated
}

// exec1 runs one allowlisted command and returns its trimmed combined output.
func (r *ExecRunner) exec1(ctx context.Context, name string, args ...string) (string, error) {
	if !r.allowed(name) {
		return "", fmt.Errorf("ExecRunner: %q is not in the allowlist", name)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = r.Dir
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("ExecRunner: %s %s: %w: %s", name, strings.Join(args, " "), err, trimmed)
	}
	return trimmed, nil
}

// Run dispatches on the mutation's Action. Selection is a two-step create+sling
// sequence executed as one at-most-once unit; every other action must be in the
// approved gated vocabulary, whose argv the runner builds itself from a validated
// target. The caller's ProposedMutation.Argv is never executed directly.
func (r *ExecRunner) Run(ctx context.Context, m ProposedMutation) (string, error) {
	if m.Action == ActionSelection {
		return r.runSelection(ctx, m)
	}
	spec, ok := r.gated()[m.Action]
	if !ok {
		return "", fmt.Errorf("ExecRunner: action %q is not in the approved gated vocabulary", m.Action)
	}
	if spec.validTarget == nil || !spec.validTarget(m.Target) {
		return "", fmt.Errorf("ExecRunner: action %q has an invalid target %q", m.Action, m.Target)
	}
	argv := spec.build(m.Target)
	return r.exec1(ctx, argv[0], argv[1:]...)
}

// runSelection mirrors bin/maintenance-cycle's create_and_sling: skip if a same-
// half bead is already open (the in-flight guard that prevents pileup across
// ticks), else create a tracking bead (body from the BodyFile path, with the
// loop-close metadata) and sling it to the polecat with --no-formula --nudge.
// Returns the created bead id, or "skipped-inflight" when the guard fires.
//
// Required Params: "polecat", "title". Optional: "priority" (default "1"),
// "labels", "half_label" (the in-flight guard label), "slack_channel"/"pl_agent"
// (loop-close metadata). Required: BodyFile (the polecat prompt, worker-local,
// never in history) — an absolute path, confined to ScratchRoot when one is set.
func (r *ExecRunner) runSelection(ctx context.Context, m ProposedMutation) (string, error) {
	polecat := m.Params["polecat"]
	title := m.Params["title"]
	if polecat == "" || title == "" {
		return "", fmt.Errorf("ExecRunner selection: params polecat and title are required")
	}
	body, err := r.validateBodyFile(m.BodyFile)
	if err != nil {
		return "", err
	}

	// In-flight guard: don't stack a second same-half bead while one is open.
	if half := m.Params["half_label"]; half != "" {
		inflight, err := r.halfInflight(ctx, half)
		if err != nil {
			return "", err
		}
		if inflight {
			return "skipped-inflight", nil
		}
	}

	priority := m.Params["priority"]
	if priority == "" {
		priority = "1"
	}
	labels := m.Params["labels"]
	if labels == "" {
		labels = "maintenance-cycle,rig:" + r.Rig
	}

	createArgs := []string{"bd", "--rig", r.Rig, "create", title,
		"--priority", priority, "--label", labels, "--body-file", body}
	if meta := loopCloseMetadata(m.Params["slack_channel"], m.Params["pl_agent"]); meta != "" {
		createArgs = append(createArgs, "--metadata", meta)
	}
	createOut, err := r.exec1(ctx, "gc", createArgs...)
	if err != nil {
		return "", err
	}
	bead := beadIDRe.FindString(createOut)
	if bead == "" {
		return "", fmt.Errorf("ExecRunner selection: no bead id in create output: %q", createOut)
	}

	if _, err := r.exec1(ctx, "gc-sling", polecat, bead, "--no-formula", "--nudge"); err != nil {
		// The bead exists but is unslung — a partial selection. The key stays
		// claimed (never retried); the created bead id is returned so it is
		// recorded structurally and the P3 reconciler can repair it.
		return bead, fmt.Errorf("ExecRunner selection: created %s but sling failed: %w", bead, err)
	}
	return bead, nil
}

// halfInflight reports whether an open bead already carries the half label, so we
// never stack a second review/author bead across ticks (mechanism, not judgment).
func (r *ExecRunner) halfInflight(ctx context.Context, halfLabel string) (bool, error) {
	out, err := r.exec1(ctx, "gc", "bd", "--rig", r.Rig, "list", "--status", "open", "--label", halfLabel, "--json")
	if err != nil {
		return false, fmt.Errorf("ExecRunner selection: in-flight check: %w", err)
	}
	// The list is "[]" (or whitespace) when nothing is open.
	trimmed := strings.TrimSpace(out)
	return trimmed != "" && trimmed != "[]" && trimmed != "null", nil
}

// loopCloseMetadata builds the metadata legacy stamped so the polecat's result
// surfaces to the maintenance Slack channel. Empty channel -> no metadata.
func loopCloseMetadata(channel, plAgent string) string {
	if channel == "" {
		return ""
	}
	// A small JSON object; no untrusted input, so a fixed template is safe.
	return fmt.Sprintf(
		`{"originating_slack":{"channel_id":%q,"thread_ts":"","user_id":""},"originating_pl_agent":%q,"loop_close_top_level":"true"}`,
		channel, plAgent)
}

// validateBodyFile requires an absolute, cleaned path and, when a ScratchRoot is
// configured, confines it to that tree.
func (r *ExecRunner) validateBodyFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("ExecRunner selection: BodyFile (polecat prompt path) is required")
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("ExecRunner selection: BodyFile must be absolute, got %q", path)
	}
	if r.ScratchRoot != "" {
		root := filepath.Clean(r.ScratchRoot)
		if clean != root && !strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			return "", fmt.Errorf("ExecRunner selection: BodyFile %q escapes scratch root %q", clean, root)
		}
	}
	return clean, nil
}
