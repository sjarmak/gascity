package maildelivery

import "github.com/gastownhall/gascity/internal/session"

// ActivationFenceFromSession adapts the lower-layer controller projection at
// the messaging boundary.
func ActivationFenceFromSession(source session.MailActivationFence) (ActivationFence, error) {
	fence := ActivationFence{
		Version: source.Version, FenceID: source.FenceID, CityRef: source.CityRef,
		SeatRef: source.SeatRef, AuthorityKind: AuthorityKind(source.AuthorityKind),
		AuthorityRef: source.AuthorityRef, AuthorityGeneration: source.AuthorityGeneration,
		AuthorityIntentSHA256: source.AuthorityIntentSHA256, SessionRef: source.SessionRef,
		ContinuationEpoch: source.ContinuationEpoch, InstanceTokenSHA256: source.InstanceTokenSHA256,
		IssuedByRef: source.IssuedByRef, IssuedAt: source.IssuedAt,
	}
	if err := fence.Validate(); err != nil {
		return ActivationFence{}, err
	}
	return fence, nil
}
