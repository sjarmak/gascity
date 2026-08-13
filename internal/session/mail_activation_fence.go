package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MailFenceAuthorityKind identifies the session controller authority projected
// for durable mail without introducing an upward dependency on messaging.
type MailFenceAuthorityKind string

const (
	// MailFenceAuthorityNamedSessionV1 is the narrow named-session controller projection.
	MailFenceAuthorityNamedSessionV1 MailFenceAuthorityKind = "named-session-controller-v1"
)

// MailActivationFence is the session-owned controller projection consumed by
// higher-layer mail adapters.
type MailActivationFence struct {
	Version               int
	FenceID               string
	CityRef               string
	SeatRef               string
	AuthorityKind         MailFenceAuthorityKind
	AuthorityRef          string
	AuthorityGeneration   uint64
	AuthorityIntentSHA256 string
	SessionRef            string
	ContinuationEpoch     uint64
	InstanceTokenSHA256   string
	IssuedByRef           string
	IssuedAt              time.Time
}

// MailActivationFenceOptions are controller-owned inputs that do not come from
// the mail sender or recipient operation.
type MailActivationFenceOptions struct {
	CityRef      string
	SeatRef      string
	ConfigSHA256 string
	IssuedByRef  string
	IssuedAt     time.Time
}

// IssueMailActivationFence projects exact persisted session authority for one
// stable seat. It does not accept generation, epoch, session ID, or token from
// the caller because those values are read from Info by the controller.
func IssueMailActivationFence(info Info, options MailActivationFenceOptions) (MailActivationFence, error) {
	state := strings.TrimSpace(info.MetadataState)
	if info.Closed || (state != string(StateActive) && state != string(StateAwake)) {
		return MailActivationFence{}, fmt.Errorf("issuing mail activation fence: session is not active")
	}
	identity := strings.TrimSpace(info.ConfiguredNamedIdentity)
	wantSeat := "seat:" + strings.TrimPrefix(options.CityRef, "city:") + "/" + identity
	if identity == "" || options.SeatRef != wantSeat {
		return MailActivationFence{}, fmt.Errorf("issuing mail activation fence: stable seat does not match session")
	}
	generation, err := parsePositiveUint(info.Generation)
	if err != nil {
		return MailActivationFence{}, fmt.Errorf("issuing mail activation fence: generation: %w", err)
	}
	epoch, err := parsePositiveUint(info.ContinuationEpoch)
	if err != nil {
		return MailActivationFence{}, fmt.Errorf("issuing mail activation fence: continuation epoch: %w", err)
	}
	if strings.TrimSpace(info.ID) == "" || strings.TrimSpace(info.InstanceToken) == "" || !isSHA256(options.ConfigSHA256) {
		return MailActivationFence{}, fmt.Errorf("issuing mail activation fence: persisted authority is incomplete")
	}
	instanceDigest := sha256Hex(info.InstanceToken)
	intent := struct {
		Version        int    `json:"version"`
		CityRef        string `json:"city_ref"`
		SeatRef        string `json:"seat_ref"`
		SessionRef     string `json:"session_ref"`
		Generation     uint64 `json:"generation"`
		Epoch          uint64 `json:"continuation_epoch"`
		InstanceSHA256 string `json:"instance_token_sha256"`
		ConfigSHA256   string `json:"config_sha256"`
		ControllerRef  string `json:"controller_ref"`
	}{1, options.CityRef, options.SeatRef, info.ID, generation, epoch, instanceDigest, options.ConfigSHA256, options.IssuedByRef}
	payload, err := json.Marshal(intent)
	if err != nil {
		return MailActivationFence{}, fmt.Errorf("issuing mail activation fence: encoding intent: %w", err)
	}
	intentDigest := sha256Hex(string(payload))
	fence := MailActivationFence{
		Version: 1, FenceID: "mail-activation-" + intentDigest,
		CityRef: options.CityRef, SeatRef: options.SeatRef,
		AuthorityKind:       MailFenceAuthorityNamedSessionV1,
		AuthorityRef:        fmt.Sprintf("mail-session-fence:%s@%d", info.ID, generation),
		AuthorityGeneration: generation, AuthorityIntentSHA256: intentDigest,
		SessionRef: info.ID, ContinuationEpoch: epoch, InstanceTokenSHA256: instanceDigest,
		IssuedByRef: options.IssuedByRef, IssuedAt: options.IssuedAt,
	}
	if err := validateMailActivationFence(fence); err != nil {
		return MailActivationFence{}, fmt.Errorf("issuing mail activation fence: %w", err)
	}
	return fence, nil
}

func validateMailActivationFence(f MailActivationFence) error {
	if f.Version != 1 || f.AuthorityKind != MailFenceAuthorityNamedSessionV1 ||
		f.AuthorityGeneration == 0 || f.ContinuationEpoch == 0 ||
		!isSHA256(strings.TrimPrefix(f.FenceID, "mail-activation-")) ||
		!isSHA256(f.AuthorityIntentSHA256) || !isSHA256(f.InstanceTokenSHA256) ||
		f.IssuedAt.IsZero() || f.IssuedAt.Location() != time.UTC {
		return fmt.Errorf("mail activation fence is invalid")
	}
	for _, ref := range []string{f.CityRef, f.SeatRef, f.AuthorityRef, f.SessionRef, f.IssuedByRef} {
		if ref == "" || strings.TrimSpace(ref) != ref || strings.ContainsAny(ref, "\x00\r\n\t") {
			return fmt.Errorf("mail activation fence reference is invalid")
		}
	}
	return nil
}

func parsePositiveUint(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("must be a positive decimal integer")
	}
	return parsed, nil
}

func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
