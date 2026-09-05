package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

type continuationRetentionState uint8

const (
	continuationAbsent continuationRetentionState = iota
	continuationRetain
	continuationUnknown
)

type continuationRetention struct {
	State continuationRetentionState
	Err   error
}

func unfinishedContinuationStatuses(sessionInfos []sessionpkg.Info, resolver qualifiedBeadResolver, resolverErr error) map[string]continuationRetention {
	statuses := make(map[string]continuationRetention, len(sessionInfos))
	for _, info := range sessionInfos {
		statuses[info.ID] = unfinishedContinuationStatus(info, resolver, resolverErr)
	}
	return statuses
}

func unfinishedContinuationStatus(info sessionpkg.Info, resolver qualifiedBeadResolver, resolverErr error) continuationRetention {
	stepID := strings.TrimSpace(info.CurrentlyProcessingBeadID)
	if stepID == "" {
		return continuationRetention{State: continuationAbsent}
	}
	if resolverErr != nil {
		return continuationRetention{State: continuationUnknown, Err: fmt.Errorf("resolve continuation for session %s: %w", info.ID, resolverErr)}
	}
	triggerID := strings.TrimSpace(info.TriggerBeadID)
	storeRef := strings.TrimSpace(info.TriggerBeadStoreRef)
	if triggerID == "" || triggerID != stepID || storeRef == "" {
		return continuationRetention{State: continuationUnknown, Err: fmt.Errorf("session %s current bead %s lacks matching qualified trigger binding", info.ID, stepID)}
	}
	step, err := resolver.Resolve(qualifiedBeadIdentity{StoreRef: storeRef, ID: stepID})
	if err != nil {
		if errors.Is(err, beads.ErrIDCollision) {
			return continuationRetention{State: continuationUnknown, Err: fmt.Errorf("session %s continuation step %s/%s: %w", info.ID, storeRef, stepID, err)}
		}
		if errors.Is(err, beads.ErrNotFound) {
			return continuationRetention{State: continuationAbsent}
		}
		return continuationRetention{State: continuationUnknown, Err: fmt.Errorf("session %s continuation step %s/%s: %w", info.ID, storeRef, stepID, err)}
	}
	if strings.TrimSpace(step.Metadata[beadmeta.ContinuationGroupMetadataKey]) == "" {
		return continuationRetention{State: continuationAbsent}
	}
	rootID := strings.TrimSpace(step.Metadata[beadmeta.RootBeadIDMetadataKey])
	rootStoreRef := strings.TrimSpace(step.Metadata[beadmeta.RootStoreRefMetadataKey])
	if rootID == "" || rootStoreRef == "" || rootStoreRef != storeRef {
		return continuationRetention{State: continuationUnknown, Err: fmt.Errorf("session %s continuation step %s/%s has incomplete root relation", info.ID, storeRef, stepID)}
	}
	root, err := resolver.Resolve(qualifiedBeadIdentity{StoreRef: rootStoreRef, ID: rootID})
	if err != nil {
		if errors.Is(err, beads.ErrIDCollision) {
			return continuationRetention{State: continuationUnknown, Err: fmt.Errorf("session %s continuation root %s/%s: %w", info.ID, rootStoreRef, rootID, err)}
		}
		if errors.Is(err, beads.ErrNotFound) {
			return continuationRetention{State: continuationAbsent}
		}
		return continuationRetention{State: continuationUnknown, Err: fmt.Errorf("session %s continuation root %s/%s: %w", info.ID, rootStoreRef, rootID, err)}
	}
	if root.Status == "closed" {
		return continuationRetention{State: continuationAbsent}
	}
	if strings.TrimSpace(root.Metadata[beadmeta.KindMetadataKey]) != beadmeta.KindWorkflow ||
		strings.TrimSpace(root.Metadata[beadmeta.FormulaContractMetadataKey]) != beadmeta.FormulaContractGraphV2 {
		return continuationRetention{State: continuationAbsent}
	}
	memberID := strings.TrimSpace(root.Metadata[beadmeta.DrainMemberIDMetadataKey])
	memberStoreRef := strings.TrimSpace(root.Metadata[beadmeta.DrainMemberStoreRefMetadataKey])
	if memberID == "" || memberStoreRef == "" {
		return continuationRetention{State: continuationUnknown, Err: fmt.Errorf("session %s continuation root %s/%s has incomplete member relation", info.ID, rootStoreRef, rootID)}
	}
	if _, err := resolver.Resolve(qualifiedBeadIdentity{StoreRef: memberStoreRef, ID: memberID}); err != nil {
		if errors.Is(err, beads.ErrIDCollision) {
			return continuationRetention{State: continuationUnknown, Err: fmt.Errorf("session %s continuation member %s/%s: %w", info.ID, memberStoreRef, memberID, err)}
		}
		if errors.Is(err, beads.ErrNotFound) {
			return continuationRetention{State: continuationAbsent}
		}
		return continuationRetention{State: continuationUnknown, Err: fmt.Errorf("session %s continuation member %s/%s: %w", info.ID, memberStoreRef, memberID, err)}
	}
	return continuationRetention{State: continuationRetain}
}
