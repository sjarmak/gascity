package temporalmaintenance

import (
	"context"
	"fmt"
)

// PRStatus is the minimal CI/review state the shim reads for one PR. Verdicts are
// already mapped from the provider's enums to our typed Verdict by the fetcher —
// a mechanical enum-to-enum transform, no judgment.
type PRStatus struct {
	CIVerdict     Verdict
	ReviewVerdict Verdict
}

// PRStatusFetcher reads a PR's current CI/review status. The real implementation
// (bridge_client.go) shells `gh` REST; tests use a fake.
type PRStatusFetcher interface {
	Fetch(ctx context.Context, repo, pr string) (PRStatus, error)
}

// CIReviewShim is the INTERIM gh-REST -> Signal bridge (promotion Phase 3). It is
// deliberately NOT a lifecycle poller like bin/pr-state-poller: it reads a PR's
// current verdict once and emits at most one Signal per branch, then stops.
// Flagged for replacement by real GitHub webhook intake in Phase 5.
type CIReviewShim struct {
	fetcher  PRStatusFetcher
	signaler *Signaler
}

// NewCIReviewShim binds the shim to a fetcher and a signaler.
func NewCIReviewShim(fetcher PRStatusFetcher, signaler *Signaler) *CIReviewShim {
	return &CIReviewShim{fetcher: fetcher, signaler: signaler}
}

// Relay reads pr's status and, if a verdict is known for branch's event kind,
// signals it once. A still-pending verdict (VerdictUnknown) sends nothing — the
// reconciler or a later Relay picks it up when it resolves. Returns whether a
// signal was sent.
func (s *CIReviewShim) Relay(ctx context.Context, repo, cycleKey, branch, pr string) (bool, error) {
	st, err := s.fetcher.Fetch(ctx, repo, pr)
	if err != nil {
		return false, err
	}
	switch branch {
	case "author":
		if st.CIVerdict == VerdictUnknown {
			return false, nil
		}
		return true, s.signaler.CICompleted(ctx, repo, cycleKey, branch, st.CIVerdict)
	case "review":
		if st.ReviewVerdict == VerdictUnknown {
			return false, nil
		}
		return true, s.signaler.ReviewCompleted(ctx, repo, cycleKey, branch, st.ReviewVerdict)
	default:
		return false, fmt.Errorf("CIReviewShim: unknown branch %q", branch)
	}
}
