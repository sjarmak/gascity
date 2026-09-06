package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/graphv2"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/storeref"
)

// hookCurrentSessionFrontDoor is the session-front-door seam `gc hook current`
// reads through, overridable in tests.
var hookCurrentSessionFrontDoor = sessionCurrentClaimFrontDoor

var hookCurrentClaimStore = resolveHookCurrentClaimStore

type hookCurrentStoreResolver func(storeRef string) (beads.Store, error)

type hookCurrentRootVarValueJSON struct {
	Name     string `json:"name"`
	Present  bool   `json:"present"`
	Encoding string `json:"encoding"`
	Value    string `json:"value"`
}

type hookCurrentRootVarJSONResult struct {
	SchemaVersion string                      `json:"schema_version"`
	OK            bool                        `json:"ok"`
	Command       string                      `json:"command"`
	SessionID     string                      `json:"session_id"`
	BeadID        string                      `json:"bead_id"`
	ClaimStoreRef string                      `json:"claim_store_ref"`
	RootBeadID    string                      `json:"root_bead_id"`
	RootStoreRef  string                      `json:"root_store_ref"`
	RootVar       hookCurrentRootVarValueJSON `json:"root_var"`
}

func newHookCurrentCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		idOnly     bool
		jsonOutput bool
		rootVar    string
	)
	cmd := &cobra.Command{
		Use:   "current",
		Short: "Print the work bead this session most recently claimed",
		Long: `Prints the work bead this session most recently claimed with gc hook --claim.

The claim protocol stamps the claimed bead id onto the calling session's own
bead, because the environment alone cannot reliably name it: $GC_BEAD_ID exists
only in the controller's dispatch condition environment, never in a session
shell, and $GC_TRIGGER_BEAD_ID — exported to demand-spawned pool seats as a
pool-level spawn marker — is absent on other seats (e.g. a warm seat bound
after start) and never decides what a session claims; the pool is pull. It
appears in the chain below only as a name fallback for work already claimed:
for a vapor wisp the trigger IS the work bead. A formula step that must close
the bead it is running reads the stamp back here:

    BEAD_ID="${GC_BEAD_ID:-${GC_TRIGGER_BEAD_ID:-$(gc hook current --id-only)}}"

The calling session is taken from $GC_SESSION_ID. Exits 1 when there is no
session identity and when the session has claimed nothing, so a caller that
cannot name its bead fails loudly instead of skipping its own work.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return exitForCode(cmdHookCurrentWithOptions(hookCurrentOptions{
				IDOnly:  idOnly,
				JSON:    jsonOutput,
				RootVar: rootVar,
			}, stdout, stderr))
		},
	}
	cmd.Flags().BoolVar(&idOnly, "id-only", false, "print only the bead id, with no surrounding context")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "emit a versioned machine-readable result")
	cmd.Flags().StringVar(&rootVar, "root-var", "", "read one graph-v2 runtime variable from the current claim's workflow root")
	return cmd
}

type hookCurrentOptions struct {
	IDOnly  bool
	JSON    bool
	RootVar string
}

// cmdHookCurrent resolves the calling session from the environment and prints
// its current claim. It is the thin env+front-door root over doHookCurrent, which
// holds the whole decision so it can be exercised without a city on disk.
func cmdHookCurrent(idOnly bool, stdout, stderr io.Writer) int {
	return cmdHookCurrentWithOptions(hookCurrentOptions{IDOnly: idOnly}, stdout, stderr)
}

func cmdHookCurrentWithOptions(opts hookCurrentOptions, stdout, stderr io.Writer) int {
	if opts.RootVar != "" && (!opts.JSON || opts.IDOnly || strings.TrimSpace(opts.RootVar) != opts.RootVar) {
		fmt.Fprintln(stderr, "gc hook current: --root-var requires --json, cannot be combined with --id-only, and must be a non-whitespace name") //nolint:errcheck
		return 1
	}
	if opts.JSON && opts.RootVar == "" {
		fmt.Fprintln(stderr, "gc hook current: --json currently requires --root-var") //nolint:errcheck
		return 1
	}
	rawSessionID := os.Getenv("GC_SESSION_ID")
	sessionID := strings.TrimSpace(rawSessionID)
	if sessionID == "" {
		fmt.Fprintln(stderr, "gc hook current: no session identity (set $GC_SESSION_ID); only a session that claimed work has a current bead") //nolint:errcheck
		return 1
	}
	if opts.RootVar != "" && rawSessionID != sessionID {
		fmt.Fprintln(stderr, "gc hook current: non-canonical session identity in $GC_SESSION_ID") //nolint:errcheck
		return 1
	}
	sessFront, err := hookCurrentSessionFrontDoor()
	if err != nil {
		fmt.Fprintf(stderr, "gc hook current: %v\n", err) //nolint:errcheck
		return 1
	}
	if opts.RootVar != "" {
		return doHookCurrentRootVar(sessFront, sessionID, opts.RootVar, hookCurrentClaimStore, stdout, stderr)
	}
	return doHookCurrent(sessFront, sessionID, opts.IDOnly, stdout, stderr)
}

func doHookCurrentRootVar(sessFront *session.Store, sessionID, varName string, resolve hookCurrentStoreResolver, stdout, stderr io.Writer) int {
	receipt, err := sessFront.CurrentClaimReceipt(sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "gc hook current: reading qualified current claim: %v\n", err) //nolint:errcheck
		return 1
	}
	if receipt.BeadID == "" {
		fmt.Fprintf(stderr, "gc hook current: session %s has no current claim (nothing claimed through gc hook --claim)\n", sessionID) //nolint:errcheck
		return 1
	}
	sessionInfo, err := sessFront.Get(sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "gc hook current: reading current session ownership: %v\n", err) //nolint:errcheck
		return 1
	}
	store, err := resolve(receipt.StoreRef)
	if err != nil {
		fmt.Fprintf(stderr, "gc hook current: resolving current claim store %q: %v\n", receipt.StoreRef, err) //nolint:errcheck
		return 1
	}
	step, err := store.Get(receipt.BeadID)
	if err != nil {
		fmt.Fprintf(stderr, "gc hook current: reading current claim %s from %s: %v\n", receipt.BeadID, receipt.StoreRef, err) //nolint:errcheck
		return 1
	}
	if step.ID != receipt.BeadID {
		fmt.Fprintf(stderr, "gc hook current: current claim lookup returned %q, want exact id %q\n", step.ID, receipt.BeadID) //nolint:errcheck
		return 1
	}
	if step.Status != "in_progress" ||
		step.Metadata[beadmeta.SessionIDMetadataKey] != sessionID ||
		!hookCurrentAssigneeOwnedBySession(step.Assignee, sessionInfo) {
		fmt.Fprintf(stderr, "gc hook current: current claim %s is no longer owned by session %s\n", receipt.BeadID, sessionID) //nolint:errcheck
		return 1
	}
	rootID := step.Metadata[beadmeta.RootBeadIDMetadataKey]
	rootStoreRef := step.Metadata[beadmeta.RootStoreRefMetadataKey]
	if rootID == "" || strings.TrimSpace(rootID) != rootID ||
		rootStoreRef == "" || strings.TrimSpace(rootStoreRef) != rootStoreRef {
		fmt.Fprintf(stderr, "gc hook current: current claim %s has no complete workflow-root relation\n", receipt.BeadID) //nolint:errcheck
		return 1
	}
	if _, known := storeref.ScopeRigContext(rootStoreRef); !known {
		fmt.Fprintf(stderr, "gc hook current: current claim %s has unknown workflow-root store ref %q\n", receipt.BeadID, rootStoreRef) //nolint:errcheck
		return 1
	}
	root, err := store.Get(rootID)
	if err != nil {
		fmt.Fprintf(stderr, "gc hook current: reading workflow root %s from %s: %v\n", rootID, receipt.StoreRef, err) //nolint:errcheck
		return 1
	}
	if root.ID != rootID || root.Metadata[beadmeta.KindMetadataKey] != beadmeta.KindWorkflow ||
		root.Metadata[beadmeta.FormulaContractMetadataKey] != beadmeta.FormulaContractGraphV2 ||
		root.Metadata[beadmeta.RootStoreRefMetadataKey] != rootStoreRef {
		fmt.Fprintf(stderr, "gc hook current: workflow root relation for %s is invalid or changed\n", receipt.BeadID) //nolint:errcheck
		return 1
	}
	vars, err := graphv2.ParseRuntimeVarsMetadata(root.Metadata[beadmeta.RuntimeVarsMetadataKey])
	if err != nil {
		fmt.Fprintf(stderr, "gc hook current: decoding workflow root variables for %s: %v\n", rootID, err) //nolint:errcheck
		return 1
	}
	value, present := vars[varName]
	confirmed, err := sessFront.CurrentClaimReceipt(sessionID)
	if err != nil || confirmed != receipt {
		fmt.Fprintf(stderr, "gc hook current: current claim changed while reading workflow root variable\n") //nolint:errcheck
		return 1
	}
	result := hookCurrentRootVarJSONResult{
		SchemaVersion: "1",
		OK:            true,
		Command:       "hook current",
		SessionID:     sessionID,
		BeadID:        receipt.BeadID,
		ClaimStoreRef: receipt.StoreRef,
		RootBeadID:    rootID,
		RootStoreRef:  rootStoreRef,
		RootVar: hookCurrentRootVarValueJSON{
			Name:     varName,
			Present:  present,
			Encoding: "base64",
			Value:    base64.StdEncoding.EncodeToString([]byte(value)),
		},
	}
	if err := writeCLIJSONLine(stdout, result); err != nil {
		fmt.Fprintf(stderr, "gc hook current: writing JSON result: %v\n", err) //nolint:errcheck
		return 1
	}
	return 0
}

func hookCurrentAssigneeOwnedBySession(assignee string, info session.Info) bool {
	for _, identity := range session.AssigneeIdentities(info) {
		if assignee == identity {
			return true
		}
	}
	return false
}

func resolveHookCurrentClaimStore(storeRef string) (beads.Store, error) {
	if storeRef == "" || strings.TrimSpace(storeRef) != storeRef {
		return nil, fmt.Errorf("invalid qualified store ref %q", storeRef)
	}
	cityPath, err := resolveCity()
	if err != nil {
		return nil, err
	}
	cfg, err := loadCityConfigWithoutBuiltinPackRefresh(cityPath, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("loading city config: %w", err)
	}
	cityName := loadedCityName(cfg, cityPath)
	switch {
	case strings.HasPrefix(storeRef, "city:"):
		if storeRef != "city:"+cityName {
			return nil, fmt.Errorf("city store ref %q does not name the resolved city %q", storeRef, cityName)
		}
		return openCityStoreAt(cityPath)
	case strings.HasPrefix(storeRef, "rig:"):
		rigName := strings.TrimPrefix(storeRef, "rig:")
		for i := range cfg.Rigs {
			if rigName != "" && cfg.Rigs[i].Name == rigName {
				if strings.TrimSpace(cfg.Rigs[i].Path) == "" {
					return nil, fmt.Errorf("rig %q has no configured store path", rigName)
				}
				rigPath := cfg.Rigs[i].Path
				if !filepath.IsAbs(rigPath) {
					rigPath = filepath.Join(cityPath, rigPath)
				}
				return openStoreAtForCityWithConfig(rigPath, cityPath, cfg)
			}
		}
		return nil, fmt.Errorf("rig store ref %q is not configured", storeRef)
	case storeref.IsClassRef(storeRef):
		binding, relocated, err := cliSoleClassBinding(cityPath)
		if err != nil {
			return nil, err
		}
		if !relocated || storeRef != string(storeref.ClassRef(binding.Classes)) {
			return nil, fmt.Errorf("class store ref %q is not the city's current binding", storeRef)
		}
		return binding.Store, nil
	default:
		return nil, fmt.Errorf("unknown qualified store ref %q", storeRef)
	}
}

// doHookCurrent prints the bead id stamped on sessionID's bead by the claim
// protocol. It exits 1 — never 0 with empty output — when nothing is stamped:
// the whole point of the back-channel is that a step which cannot name its own
// bead must fail loudly rather than let a caller substitute an empty string and
// skip the close it owes.
func doHookCurrent(sessFront *session.Store, sessionID string, idOnly bool, stdout, stderr io.Writer) int {
	beadID, err := sessFront.CurrentClaimBeadID(sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "gc hook current: %v\n", err) //nolint:errcheck
		return 1
	}
	if beadID == "" {
		fmt.Fprintf(stderr, "gc hook current: session %s has no current claim (nothing claimed through gc hook --claim)\n", sessionID) //nolint:errcheck
		return 1
	}
	if idOnly {
		fmt.Fprintln(stdout, beadID) //nolint:errcheck
		return 0
	}
	fmt.Fprintf(stdout, "%s (claimed by session %s)\n", beadID, sessionID) //nolint:errcheck
	return 0
}
