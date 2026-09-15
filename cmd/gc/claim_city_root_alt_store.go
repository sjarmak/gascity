package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/executionevent"
)

type mixedCityRootClaimRoute struct {
	store    beads.Store
	resident map[string]bool
}

func mixedCityRootHookClaimOps(cityPath string, ops hookClaimOps) (hookClaimOps, error) {
	if !cityRootIsMixed(cityPath) {
		return ops, nil
	}
	configured, err := openStoreAtForCity(cityPath, cityPath)
	if err != nil {
		return ops, err
	}
	route := &mixedCityRootClaimRoute{store: configured, resident: make(map[string]bool)}
	ops.applyDefaults()
	base := ops

	ops.Claim = func(ctx context.Context, dir string, env []string, beadID, assignee string) (beads.Bead, bool, error) {
		claimed, ok, claimErr := base.Claim(ctx, dir, env, beadID, assignee)
		if ok || !errors.Is(claimErr, beads.ErrNotFound) || !samePath(dir, cityPath) {
			return claimed, ok, claimErr
		}
		claimed, ok, claimErr = route.claim(beadID, assignee)
		if ok {
			route.remember(claimed)
		}
		return claimed, ok, claimErr
	}
	ops.StampWorkMeta = func(ctx context.Context, dir string, env []string, beadID, assignee string, patch map[string]string) error {
		if route.resident[beadID] {
			return route.store.Update(beadID, beads.UpdateOpts{Metadata: patch})
		}
		return base.StampWorkMeta(ctx, dir, env, beadID, assignee, patch)
	}
	ops.ReadWorkMeta = func(ctx context.Context, dir string, env []string, beadID, assignee string) (beads.Bead, error) {
		if route.resident[beadID] {
			return route.store.Get(beadID)
		}
		return base.ReadWorkMeta(ctx, dir, env, beadID, assignee)
	}
	ops.ListContinuation = func(ctx context.Context, dir string, env []string, rootID, group string) ([]beads.Bead, error) {
		if !route.resident[rootID] {
			return base.ListContinuation(ctx, dir, env, rootID, group)
		}
		siblings, listErr := route.store.List(beads.ListQuery{
			Status: "open",
			Metadata: map[string]string{
				beadmeta.RootBeadIDMetadataKey:        rootID,
				beadmeta.ContinuationGroupMetadataKey: group,
			},
			TierMode: beads.TierBoth,
		})
		for _, sibling := range siblings {
			route.remember(sibling)
		}
		return siblings, listErr
	}
	ops.AssignContinuation = func(ctx context.Context, dir string, env []string, beadID, assignee string) error {
		if route.resident[beadID] {
			return route.store.Update(beadID, beads.UpdateOpts{Assignee: &assignee})
		}
		return base.AssignContinuation(ctx, dir, env, beadID, assignee)
	}
	ops.Release = func(ctx context.Context, dir string, env []string, beadID, assignee string) (bool, error) {
		if route.resident[beadID] {
			releaser, ok := route.store.(beads.ConditionalAssignmentReleaser)
			if !ok {
				return false, fmt.Errorf("configured city store %T cannot conditionally release assignments", route.store)
			}
			return releaser.ReleaseIfCurrent(beadID, assignee)
		}
		return base.Release(ctx, dir, env, beadID, assignee)
	}
	ops.EmitExecutionStepStarted = func(step beads.Bead, dir string, env []string, assignee, storeRef string) {
		if !route.resident[step.ID] {
			base.EmitExecutionStepStarted(step, dir, env, assignee, storeRef)
			return
		}
		rec := openCityRecorder(io.Discard)
		if closer, ok := rec.(io.Closer); ok {
			defer closer.Close() //nolint:errcheck
		}
		store := executionevent.WithStoreRef(route.store, storeRef)
		_ = executionevent.EmitLifecycle(rec, store, events.ExecutionStepStarted, step, eventActor())
	}
	return ops, nil
}

func (r *mixedCityRootClaimRoute) claim(beadID, assignee string) (beads.Bead, bool, error) {
	return hookClaimThroughStore(beadID, assignee, func() (beads.Bead, bool, error) {
		current, err := r.store.Get(beadID)
		if err != nil {
			return beads.Bead{}, false, err
		}
		owner := strings.TrimSpace(current.Assignee)
		currentStatus := strings.ToLower(strings.TrimSpace(current.Status))
		if (currentStatus != "open" && currentStatus != "in_progress") || (owner != "" && owner != assignee) {
			return current, false, nil
		}
		if currentStatus == "in_progress" && owner == assignee {
			return current, true, nil
		}
		writeStore, _, _ := unwrapBeadPolicyStore(r.store)
		writer, ok := beads.ConditionalWriterFor(writeStore)
		if !ok {
			return beads.Bead{}, false, fmt.Errorf("configured city store %T has no conditional assignment writer", r.store)
		}
		status := "in_progress"
		if err := writer.UpdateIfMatch(beadID, current.Revision, beads.UpdateOpts{Status: &status, Assignee: &assignee}); err != nil {
			if beads.IsPreconditionFailed(err) {
				return beads.Bead{}, false, nil
			}
			return beads.Bead{}, false, err
		}
		current.Status = status
		current.Assignee = assignee
		return current, true, nil
	}, r.store.Get)
}

func (r *mixedCityRootClaimRoute) remember(bead beads.Bead) {
	if id := strings.TrimSpace(bead.ID); id != "" {
		r.resident[id] = true
	}
	if rootID := strings.TrimSpace(bead.Metadata[beadmeta.RootBeadIDMetadataKey]); rootID != "" {
		r.resident[rootID] = true
	}
}
