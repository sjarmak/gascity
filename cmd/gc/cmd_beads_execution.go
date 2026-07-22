package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/execview"
	"github.com/gastownhall/gascity/internal/git"
)

// cmdBeadsShowExecution renders the read-only execution projection for a work
// bead (`gc beads show <id> --execution`). Unlike the plain show path it is
// local-only: it correlates the bead with its active workflow(s), current
// step, live session, and target-worktree git state, none of which the
// supervisor read API projects today. It reads live store state directly
// (across every rig/city store) and never mutates any state.
func cmdBeadsShowExecution(id, format string, stdout, stderr io.Writer) int {
	if id == "" {
		fmt.Fprintln(stderr, "gc beads show: missing bead id") //nolint:errcheck // best-effort stderr
		return 1
	}
	_, isRemote, cityPath, err := resolveReadTarget()
	if err != nil {
		fmt.Fprintf(stderr, "gc beads show --execution: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	if isRemote {
		fmt.Fprintln(stderr, "gc beads show --execution: not available against a remote city (needs local session and worktree state)") //nolint:errcheck // best-effort stderr
		return 1
	}

	cfg, err := loadCityConfig(cityPath, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "gc beads show --execution: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	views, code := openAllConvoyStoresAt(cityPath, stderr, "gc beads show --execution")
	if views == nil {
		return code
	}
	stores := make([]beads.Store, 0, len(views))
	for _, v := range views {
		stores = append(stores, v.store)
	}
	workBead, found := findExecutionWorkBead(stores, id, stderr)
	if !found {
		return 1
	}

	sessions := newExecutionSessionLookup(cityPath, cfg)
	proj := execview.Build(stores, workBead, sessions, gitWorktreeProbe{})

	if format == "json" {
		return renderExecutionJSON(proj, stdout, stderr)
	}
	renderExecutionText(proj, stdout)
	return 0
}

// findExecutionWorkBead returns the first store's copy of id. A read error
// other than not-found is reported and treated as failure; a bead present in
// no store prints a not-found message. The full store slice (not just the
// owning one) is later handed to Build so cross-store workflow roots are found.
func findExecutionWorkBead(stores []beads.Store, id string, stderr io.Writer) (beads.Bead, bool) {
	for _, store := range stores {
		b, err := store.Get(id)
		if err != nil {
			if errors.Is(err, beads.ErrNotFound) {
				continue
			}
			fmt.Fprintf(stderr, "gc beads show --execution: %v\n", err) //nolint:errcheck // best-effort stderr
			return beads.Bead{}, false
		}
		return b, true
	}
	fmt.Fprintf(stderr, "gc beads show --execution: bead %s not found\n", id) //nolint:errcheck // best-effort stderr
	return beads.Bead{}, false
}

// sessionMapLookup is a read-only execview.SessionLookup backed by a snapshot
// of live sessions keyed by tmux session name.
type sessionMapLookup map[string]execview.SessionView

func (s sessionMapLookup) Lookup(name string) (execview.SessionView, bool) {
	v, ok := s[name]
	return v, ok
}

// newExecutionSessionLookup builds a best-effort, read-only session lookup from
// the worker session catalog's list path (which reads persisted session beads
// without the empty-type heal write that the by-id Get path performs). If the
// catalog cannot be constructed or listed, it returns nil; execview.Build then
// omits the session section with a warning rather than failing the projection.
func newExecutionSessionLookup(cityPath string, cfg *config.City) execview.SessionLookup {
	store, code := openCityStore(io.Discard, "gc beads show --execution")
	if store == nil || code != 0 {
		return nil
	}
	sessStore := cliSessionStore(store, cfg, cityPath)
	sp, err := newSessionProvider()
	if err != nil {
		return nil
	}
	catalog, err := workerSessionCatalogWithConfig(cityPath, sessStore, sp, cfg)
	if err != nil {
		return nil
	}
	infos, err := catalog.List("", "")
	if err != nil {
		return nil
	}
	m := make(sessionMapLookup, len(infos))
	for _, info := range infos {
		var lastActive *time.Time
		if !info.LastActive.IsZero() {
			la := info.LastActive
			lastActive = &la
		}
		m[info.SessionName] = execview.SessionView{
			State:      string(info.State),
			LastActive: lastActive,
		}
	}
	return m
}

// gitWorktreeProbe is the read-only execview.WorktreeProbe backed by
// internal/git.
type gitWorktreeProbe struct{}

func (gitWorktreeProbe) Probe(dir, baseRef string) execview.WorktreeView {
	v := execview.WorktreeView{Path: dir, CommitsAhead: -1}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return v // Present=false
	}
	v.Present = true

	g := git.New(dir)
	if !g.IsRepo() {
		return v // IsGit=false
	}
	v.IsGit = true
	if branch, err := g.CurrentBranch(); err == nil {
		v.Branch = branch
	}
	v.Dirty = g.HasUncommittedWork()
	head, err := g.Head()
	if err != nil {
		v.Err = err.Error()
		return v
	}
	v.Head = head

	base := resolveIntegrationBase(g, baseRef)
	if base == "" {
		return v // leave CommitsAhead=-1, ReachableFromMain=false
	}
	if ahead, err := g.CommitsAhead(base); err == nil {
		v.CommitsAhead = ahead
	}
	if reachable, err := g.IsAncestor("HEAD", base); err == nil {
		v.ReachableFromMain = reachable
	}
	return v
}

// resolveIntegrationBase picks the base ref to measure ahead/merge state
// against: the caller-provided baseRef when it resolves, else the repository's
// default branch (origin/<default> preferred over the local <default>). Returns
// "" when nothing resolves, so the probe skips ahead/merge fields rather than
// reporting against a hardcoded, possibly-wrong base.
func resolveIntegrationBase(g *git.Git, baseRef string) string {
	var candidates []string
	if b := strings.TrimSpace(baseRef); b != "" {
		candidates = append(candidates, b)
	}
	if def := g.ProbeDefaultBranch(); def != "" {
		candidates = append(candidates, "origin/"+def, def)
	}
	for _, ref := range candidates {
		if resolvesRef(g, ref) {
			return ref
		}
	}
	return ""
}

// resolvesRef reports whether ref names a resolvable commit. A commit is its
// own ancestor, so a clean is-ancestor(ref, ref) means ref resolves; an
// unresolvable ref returns an error.
func resolvesRef(g *git.Git, ref string) bool {
	ok, err := g.IsAncestor(ref, ref)
	return err == nil && ok
}

func renderExecutionJSON(p execview.Projection, stdout, stderr io.Writer) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(p); err != nil {
		fmt.Fprintf(stderr, "gc beads show --execution: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	return 0
}

// renderExecutionText writes a compact, diagnostic rendering of the projection.
func renderExecutionText(p execview.Projection, w io.Writer) {
	fp := func(format string, a ...any) { fmt.Fprintf(w, format, a...) } //nolint:errcheck // best-effort stdout

	wb := p.WorkBead
	fp("work bead %s  status=%s", wb.ID, wb.Status)
	if wb.Priority != nil {
		fp("  priority=%d", *wb.Priority)
	}
	if wb.Assignee != "" {
		fp("  assignee=%s", wb.Assignee)
	}
	if wb.Title != "" {
		fp("  title=%q", wb.Title)
	}
	fp("\n")

	if len(p.Workflows) == 0 {
		fp("workflows: none\n")
	} else {
		fp("workflows:\n")
		for _, wf := range p.Workflows {
			fp("  %s  status=%s", wf.RootID, wf.Status)
			if wf.Formula != "" {
				fp("  formula=%s", wf.Formula)
			}
			fp("\n")
			if wf.CurrentStep != nil {
				s := wf.CurrentStep
				fp("    current step: %s  status=%s", s.ID, s.Status)
				if s.StepRef != "" {
					fp("  %s", s.StepRef)
				}
				if s.Assignee != "" {
					fp("  assignee=%s", s.Assignee)
				}
				if s.Runnable {
					fp("  (runnable)")
				}
				fp("\n")
			} else {
				fp("    current step: none\n")
			}
			if wf.RootSession != "" {
				fp("    root session: %s\n", wf.RootSession)
			}
		}
	}

	if p.Session != nil {
		s := p.Session
		fp("session: %s", s.Name)
		if s.Live {
			fp("  state=%s", s.State)
			if s.LastActive != nil {
				fp("  last_active=%s", s.LastActive.UTC().Format(time.RFC3339))
			}
			fp("  (live)")
		} else {
			fp("  (not live)")
		}
		fp("\n")
	} else {
		fp("session: none\n")
	}

	if p.Worktree != nil {
		wt := p.Worktree
		fp("worktree: %s  (%s)\n", wt.Path, wt.Source)
		switch {
		case !wt.Present:
			fp("    absent\n")
		case !wt.IsGit:
			fp("    not a git repository\n")
		default:
			ahead := "unknown"
			if wt.CommitsAhead >= 0 {
				ahead = fmt.Sprintf("%d", wt.CommitsAhead)
			}
			fp("    branch=%s  head=%s  dirty=%t  ahead=%s  reachable_from_main=%t\n",
				wt.Branch, shortSHA(wt.Head), wt.Dirty, ahead, wt.ReachableFromMain)
		}
	} else {
		fp("worktree: none\n")
	}

	if len(p.Warnings) > 0 {
		fp("warnings:\n")
		for _, warn := range p.Warnings {
			fp("  - %s\n", warn)
		}
	}
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
