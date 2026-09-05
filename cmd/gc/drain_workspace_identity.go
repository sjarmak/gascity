package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/storeref"
)

// qualifiedStoreBinding describes where a logical store reference may be
// resolved. A class binding is physical residency shared by multiple logical
// scopes, so its own ref must never be mistaken for the bead's StoreRef.
type qualifiedStoreBinding struct {
	StoreRef     string
	Store        beads.Store
	ClassBinding bool
}

type qualifiedBeadResolver struct {
	bindings []qualifiedStoreBinding
}

func newQualifiedBeadResolver(bindings []qualifiedStoreBinding) qualifiedBeadResolver {
	return qualifiedBeadResolver{bindings: append([]qualifiedStoreBinding(nil), bindings...)}
}

func qualifiedStoreRefMatches(candidate, qualified string) bool {
	candidate = strings.TrimSpace(candidate)
	qualified = strings.TrimSpace(qualified)
	if candidate == qualified {
		return candidate != ""
	}
	if candidate == "city" {
		return strings.HasPrefix(qualified, "city:")
	}
	if !strings.Contains(candidate, ":") {
		return qualified == "rig:"+candidate
	}
	return false
}

func (r qualifiedBeadResolver) Resolve(identity qualifiedBeadIdentity) (beads.Bead, error) {
	id := strings.TrimSpace(identity.ID)
	storeRef := strings.TrimSpace(identity.StoreRef)
	if id == "" || storeRef == "" {
		return beads.Bead{}, fmt.Errorf("qualified bead identity requires store ref and id")
	}

	var match *beads.Bead
	seenStores := make(map[string]struct{})
	for _, binding := range r.bindings {
		if binding.Store == nil || (!binding.ClassBinding && !qualifiedStoreRefMatches(binding.StoreRef, storeRef)) {
			continue
		}
		storeKey := fmt.Sprintf("%T:%p", binding.Store, binding.Store)
		if _, duplicate := seenStores[storeKey]; duplicate {
			continue
		}
		seenStores[storeKey] = struct{}{}
		bead, err := binding.Store.Get(id)
		if err != nil {
			if errors.Is(err, beads.ErrNotFound) {
				continue
			}
			return beads.Bead{}, fmt.Errorf("resolve %s/%s via %s: %w", storeRef, id, binding.StoreRef, err)
		}
		actualRef := strings.TrimSpace(bead.Metadata[beadmeta.RootStoreRefMetadataKey])
		if actualRef != storeRef {
			continue
		}
		if match != nil {
			return beads.Bead{}, fmt.Errorf("resolve %s/%s: ambiguous across physical stores", storeRef, id)
		}
		beadCopy := bead
		match = &beadCopy
	}
	if match == nil {
		return beads.Bead{}, fmt.Errorf("resolve %s/%s: %w", storeRef, id, beads.ErrNotFound)
	}
	return *match, nil
}

func qualifiedBindingsFromCandidates(candidates []classStoreCandidate) []qualifiedStoreBinding {
	bindings := make([]qualifiedStoreBinding, 0, len(candidates))
	for _, candidate := range candidates {
		bindings = append(bindings, qualifiedStoreBinding{
			StoreRef:     candidate.ref,
			Store:        candidate.store,
			ClassBinding: storeref.IsClassRef(candidate.ref),
		})
	}
	return bindings
}

// drainWorkspaceBindingForExecution follows the validated materialized-step
// relation to its workflow root and then to the drain member. Ownership
// evidence is always read from the member's current row.
func drainWorkspaceBindingForExecution(execution beads.Bead, executionStoreRef string, resolver qualifiedBeadResolver) (*poolWorktreeBinding, error) {
	executionID := qualifiedBeadIdentity{StoreRef: strings.TrimSpace(executionStoreRef), ID: strings.TrimSpace(execution.ID)}
	if executionID.StoreRef == "" {
		executionID.StoreRef = strings.TrimSpace(execution.Metadata[beadmeta.RootStoreRefMetadataKey])
	}
	if actual := strings.TrimSpace(execution.Metadata[beadmeta.RootStoreRefMetadataKey]); actual == "" || actual != executionID.StoreRef {
		return nil, fmt.Errorf("drain execution %s store ref %q does not match request %q", execution.ID, actual, executionID.StoreRef)
	}
	rootID := strings.TrimSpace(execution.Metadata[beadmeta.RootBeadIDMetadataKey])
	if rootID == "" {
		return nil, nil
	}
	root, err := resolver.Resolve(qualifiedBeadIdentity{StoreRef: executionID.StoreRef, ID: rootID})
	if err != nil {
		return nil, fmt.Errorf("drain execution %s root %s: %w", execution.ID, rootID, err)
	}
	if strings.TrimSpace(root.Metadata[beadmeta.KindMetadataKey]) != beadmeta.KindWorkflow ||
		strings.TrimSpace(root.Metadata[beadmeta.FormulaContractMetadataKey]) != beadmeta.FormulaContractGraphV2 {
		return nil, nil
	}
	memberID := strings.TrimSpace(root.Metadata[beadmeta.DrainMemberIDMetadataKey])
	memberStoreRef := strings.TrimSpace(root.Metadata[beadmeta.DrainMemberStoreRefMetadataKey])
	if memberID == "" && memberStoreRef == "" {
		return nil, nil
	}
	if memberID == "" || memberStoreRef == "" {
		return nil, fmt.Errorf("drain root %s has incomplete qualified member relation", root.ID)
	}
	ownerID := qualifiedBeadIdentity{StoreRef: memberStoreRef, ID: memberID}
	member, err := resolver.Resolve(ownerID)
	if err != nil {
		return nil, fmt.Errorf("drain root %s member %s/%s: %w", root.ID, memberStoreRef, memberID, err)
	}
	spec, err := worktreeSpecForBead(member, memberStoreRef)
	if err != nil {
		return nil, fmt.Errorf("drain member %s/%s workspace: %w", memberStoreRef, memberID, err)
	}
	if spec == nil {
		return nil, nil
	}
	return &poolWorktreeBinding{Execution: executionID, Root: qualifiedBeadIdentity{StoreRef: executionID.StoreRef, ID: rootID}, Owner: ownerID, Spec: *spec}, nil
}

// revalidateDrainWorkspaceBinding rejects changed authority rather than
// upgrading an earlier request to whichever workspace is current now.
func revalidateDrainWorkspaceBinding(bp *agentBuildParams, expected *poolWorktreeBinding) error {
	if bp == nil || bp.beadStore == nil {
		return fmt.Errorf("drain workspace destination store unavailable")
	}
	candidates, err := censusStoreCandidates(bp.cityPath, bp.city, bp.beadStore, bp.sessionCensusRigStores, bp.sessionCensusSuspendedRigPaths, censusRefScoped)
	if err != nil {
		return fmt.Errorf("drain workspace destination topology: %w", err)
	}
	resolver := newQualifiedBeadResolver(qualifiedBindingsFromCandidates(candidates))
	execution, err := resolver.Resolve(expected.Execution)
	if err != nil {
		return fmt.Errorf("drain workspace destination execution: %w", err)
	}
	current, err := drainWorkspaceBindingForExecution(execution, expected.Execution.StoreRef, resolver)
	if err != nil {
		return fmt.Errorf("drain workspace destination relation: %w", err)
	}
	if current == nil || *current != *expected {
		return fmt.Errorf("drain workspace destination relation or owner evidence changed")
	}
	return nil
}
