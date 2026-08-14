package beadmail

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/mail"
	"github.com/gastownhall/gascity/internal/maildelivery"
)

const durableDeliveryRepairKey = "mail.delivery.repair.v1"

const stableDurableSendKeyMaxBytes = 128

// StableDurableSendIntent names a bounded, replay-stable durable notification.
// The logical city, messaging authority, and seat are explicit so message
// identity cannot confuse the messaging and sessions coordination classes.
type StableDurableSendIntent struct {
	CityRef           string
	MessagingStoreRef string
	SeatRef           string
	StableKey         string
	Policy            maildelivery.Policy
	Attention         maildelivery.Attention
	ObservedAt        time.Time
}

// DurableSendIntent is the content-free, preallocated identity and policy for
// a recoverable message-first send.
type DurableSendIntent struct {
	MessageID          string
	StoreRef           string
	SeatRef            string
	Policy             maildelivery.Policy
	Attention          maildelivery.Attention
	ExpiresAt          *time.Time
	PolicySourceSHA256 string
	// ObservedAt is the caller's UTC observation time for expiry validation.
	// It is neither persisted nor part of delivery identity; zero uses the
	// current UTC time.
	ObservedAt time.Time
}

type durableDeliveryRepair struct {
	Version            int                    `json:"version"`
	MessageID          string                 `json:"message_id"`
	StoreRef           string                 `json:"store_ref"`
	SeatRef            string                 `json:"seat_ref"`
	Policy             maildelivery.Policy    `json:"policy"`
	Attention          maildelivery.Attention `json:"attention"`
	ExpiresAt          *time.Time             `json:"expires_at,omitempty"`
	PolicySourceSHA256 string                 `json:"policy_source_sha256,omitempty"`
}

// SendDurableStable derives the explicit message identity inside the beadmail
// boundary and delegates to the message-first durable write protocol.
func (p *Provider) SendDurableStable(from, to, subject, body string, intent StableDurableSendIntent) (mail.Message, maildelivery.Delivery, error) {
	messageID, err := durableStableMessageID(intent)
	if err != nil {
		return mail.Message{}, maildelivery.Delivery{}, fmt.Errorf("beadmail stable durable send: %w", err)
	}
	city := strings.TrimPrefix(intent.CityRef, "city:")
	seatOwner := strings.TrimPrefix(intent.SeatRef, "seat:"+city+"/")
	if to != seatOwner {
		return mail.Message{}, maildelivery.Delivery{}, fmt.Errorf("beadmail stable durable send: %w: recipient %q does not match stable seat %q", maildelivery.ErrConflict, to, intent.SeatRef)
	}
	return p.SendDurable(from, to, subject, body, DurableSendIntent{
		MessageID: messageID, StoreRef: intent.MessagingStoreRef, SeatRef: intent.SeatRef,
		Policy: intent.Policy, Attention: intent.Attention, ObservedAt: intent.ObservedAt,
	})
}

func durableStableMessageID(intent StableDurableSendIntent) (string, error) {
	if !validStableDurableSendKey(intent.StableKey) {
		return "", fmt.Errorf("stable key must be 1-%d ASCII identifier bytes", stableDurableSendKeyMaxBytes)
	}
	city := strings.TrimPrefix(intent.CityRef, "city:")
	if city == "" || intent.CityRef != "city:"+city || strings.ContainsAny(city, "/\x00\r\n") {
		return "", fmt.Errorf("city ref %q is invalid", intent.CityRef)
	}
	if intent.MessagingStoreRef != intent.CityRef+"/messaging" {
		return "", fmt.Errorf("messaging store ref %q does not belong to %q", intent.MessagingStoreRef, intent.CityRef)
	}
	seatPrefix := "seat:" + city + "/"
	seatOwner := strings.TrimPrefix(intent.SeatRef, seatPrefix)
	if seatOwner == "" || intent.SeatRef != seatPrefix+seatOwner || strings.ContainsAny(seatOwner, "\x00\r\n") {
		return "", fmt.Errorf("seat ref %q does not belong to %q", intent.SeatRef, intent.CityRef)
	}
	sum := sha256.Sum256([]byte("gascity-mail-durable-notify-v1\x00" + intent.CityRef + "\x00" + intent.MessagingStoreRef + "\x00" + intent.SeatRef + "\x00" + intent.StableKey))
	return fmt.Sprintf("gc-mail-%x", sum[:16]), nil
}

func validStableDurableSendKey(key string) bool {
	if len(key) == 0 || len(key) > stableDurableSendKeyMaxBytes {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' || c == '/' || c == '-' {
			continue
		}
		return false
	}
	return true
}

// SendDurable persists a message carrying complete content-free repair intent,
// then creates its canonical delivery. A second-write failure returns the
// durable message so callers can report the honest message-only state.
func (p *Provider) SendDurable(from, to, subject, body string, intent DurableSendIntent) (mail.Message, maildelivery.Delivery, error) {
	if p == nil || p.store == nil {
		return mail.Message{}, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: store unavailable")
	}
	if to == "" {
		return mail.Message{}, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: recipient is required")
	}
	repair := durableDeliveryRepair{
		Version: 1, MessageID: intent.MessageID, StoreRef: intent.StoreRef,
		SeatRef: intent.SeatRef, Policy: intent.Policy, Attention: intent.Attention,
		ExpiresAt: intent.ExpiresAt, PolicySourceSHA256: intent.PolicySourceSHA256,
	}
	repairJSON, err := json.Marshal(repair)
	if err != nil {
		return mail.Message{}, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: encoding repair intent: %w", err)
	}
	from, metadata, err := p.resolveSenderRoute(from)
	if err != nil {
		return mail.Message{}, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: %w", err)
	}
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata[durableDeliveryRepairKey] = string(repairJSON)
	threadID := durableThreadID(intent.MessageID)
	title := subject
	if title == "" && body != "" {
		title = strings.SplitN(body, "\n", 2)[0]
		if len(title) > 80 {
			title = title[:77] + "..."
		}
	}
	wanted := beads.Bead{
		ID: intent.MessageID, Title: title, Description: body, Type: messageBeadType,
		Assignee: to, From: from, Labels: []string{"thread:" + threadID}, Metadata: metadata,
		NoHistory: true,
	}
	// An exact durable replay remains valid after its immutable expiry. Resolve
	// that case before validating a prospective new write against the current
	// clock; otherwise retrying a completed message-first operation would change
	// from success to failure merely because time advanced.
	existing, getErr := p.store.Get(intent.MessageID)
	if getErr == nil {
		if !sameDurableMessage(existing, wanted) {
			return mail.Message{}, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: %w: message %q already exists with different bytes", maildelivery.ErrConflict, intent.MessageID)
		}
		message := beadToMessage(existing)
		delivery, deliveryErr := deliveryFromMessageRow(existing, repair)
		if deliveryErr != nil {
			return message, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: %w", deliveryErr)
		}
		created, createErr := ensureDurableDelivery(maildelivery.NewStore(p.store), delivery)
		if createErr != nil {
			return message, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: creating delivery: %w", createErr)
		}
		return message, created, nil
	}
	if !errors.Is(getErr, beads.ErrNotFound) {
		return mail.Message{}, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: checking message replay: %w", getErr)
	}

	// Validate every caller-controlled scalar, including expiry against the
	// injected/current UTC clock, before the single message-write edge.
	validationTime := intent.ObservedAt
	if validationTime.IsZero() {
		validationTime = time.Now().UTC()
	}
	if _, err := maildelivery.NewDelivery(intent.StoreRef, intent.MessageID, 1, intent.SeatRef, intent.Policy, intent.Attention, validationTime, intent.ExpiresAt, intent.PolicySourceSHA256); err != nil {
		return mail.Message{}, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: %w", err)
	}
	row, err := p.store.Create(wanted)
	if err != nil {
		existing, getErr := p.store.Get(intent.MessageID)
		if getErr != nil {
			return mail.Message{}, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: creating message: %w", err)
		}
		if !sameDurableMessage(existing, wanted) {
			return mail.Message{}, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: %w: message %q already exists with different bytes", maildelivery.ErrConflict, intent.MessageID)
		}
		row = existing
	}
	message := beadToMessage(row)
	if row.ID != intent.MessageID {
		return message, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: store changed deterministic message ID %q to %q", intent.MessageID, row.ID)
	}
	delivery, err := deliveryFromMessageRow(row, repair)
	if err != nil {
		return message, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: %w", err)
	}
	created, err := ensureDurableDelivery(maildelivery.NewStore(p.store), delivery)
	if err != nil {
		return message, maildelivery.Delivery{}, fmt.Errorf("beadmail durable send: creating delivery: %w", err)
	}
	return message, created, nil
}

func durableThreadID(messageID string) string {
	sum := sha256.Sum256([]byte("mail-thread-v1\x00" + messageID))
	return fmt.Sprintf("mail-thread-%x", sum[:])
}

func sameDurableMessage(existing, wanted beads.Bead) bool {
	if existing.ID != wanted.ID || existing.Title != wanted.Title ||
		existing.Description != wanted.Description || existing.Type != wanted.Type ||
		existing.Assignee != wanted.Assignee || existing.From != wanted.From {
		return false
	}
	if !existing.NoHistory || existing.Ephemeral {
		return false
	}
	for key, value := range wanted.Metadata {
		if existing.Metadata[key] != value {
			return false
		}
	}
	wantThread := durableThreadID(wanted.ID)
	for _, label := range existing.Labels {
		if label == "thread:"+wantThread {
			return true
		}
	}
	return false
}

// RepairDurableDelivery reconstructs the deterministic delivery only from the
// exact message row and its immutable repair metadata.
func (p *Provider) RepairDurableDelivery(messageID string) (maildelivery.Delivery, error) {
	if p == nil || p.store == nil {
		return maildelivery.Delivery{}, fmt.Errorf("beadmail repair delivery: store unavailable")
	}
	row, err := p.store.Get(messageID)
	if err != nil {
		return maildelivery.Delivery{}, fmt.Errorf("beadmail repair delivery: getting message: %w", err)
	}
	if row.Type != messageBeadType {
		return maildelivery.Delivery{}, fmt.Errorf("beadmail repair delivery: %q is not a message", messageID)
	}
	repair, err := decodeDurableRepair(row.Metadata[durableDeliveryRepairKey])
	if err != nil {
		return maildelivery.Delivery{}, fmt.Errorf("beadmail repair delivery: %w", err)
	}
	delivery, err := deliveryFromMessageRow(row, repair)
	if err != nil {
		return maildelivery.Delivery{}, fmt.Errorf("beadmail repair delivery: %w", err)
	}
	return ensureDurableDelivery(maildelivery.NewStore(p.store), delivery)
}

func deliveryFromMessageRow(row beads.Bead, repair durableDeliveryRepair) (maildelivery.Delivery, error) {
	if repair.Version != 1 || repair.MessageID != row.ID {
		return maildelivery.Delivery{}, fmt.Errorf("repair identity does not match message")
	}
	if row.Revision <= 0 {
		return maildelivery.Delivery{}, fmt.Errorf("message revision is invalid")
	}
	cityRef := strings.TrimSuffix(repair.StoreRef, "/messaging")
	city := strings.TrimPrefix(cityRef, "city:")
	if city == "" || cityRef != "city:"+city || repair.StoreRef != cityRef+"/messaging" {
		return maildelivery.Delivery{}, fmt.Errorf("repair messaging store identity is invalid")
	}
	wantSeatRef := "seat:" + city + "/" + row.Assignee
	if row.Assignee == "" || repair.SeatRef != wantSeatRef {
		return maildelivery.Delivery{}, fmt.Errorf("repair seat %q does not match message assignee %q in city %q", repair.SeatRef, row.Assignee, city)
	}
	return maildelivery.NewDelivery(repair.StoreRef, row.ID, uint64(row.Revision), repair.SeatRef, repair.Policy, repair.Attention, row.CreatedAt.UTC(), repair.ExpiresAt, repair.PolicySourceSHA256)
}

func ensureDurableDelivery(store *maildelivery.Store, wanted maildelivery.Delivery) (maildelivery.Delivery, error) {
	created, err := store.Create(wanted)
	if err == nil {
		return created, nil
	}
	current, getErr := store.Get(wanted.ID)
	if getErr == nil && sameDurableDeliveryIntent(current, wanted) {
		return current, nil
	}
	return maildelivery.Delivery{}, err
}

func sameDurableDeliveryIntent(current, wanted maildelivery.Delivery) bool {
	if (current.ExpiresAt == nil) != (wanted.ExpiresAt == nil) {
		return false
	}
	if current.ExpiresAt != nil && !current.ExpiresAt.Equal(*wanted.ExpiresAt) {
		return false
	}
	current.ExpiresAt = nil
	wanted.ExpiresAt = nil
	current.Phase, wanted.Phase = "", ""
	current.Revision, wanted.Revision = 0, 0
	current.MessageRevision, wanted.MessageRevision = 0, 0
	return current == wanted
}

func decodeDurableRepair(data string) (durableDeliveryRepair, error) {
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	var repair durableDeliveryRepair
	if err := decoder.Decode(&repair); err != nil {
		return durableDeliveryRepair{}, fmt.Errorf("decoding repair intent: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return durableDeliveryRepair{}, fmt.Errorf("decoding repair intent: trailing JSON")
	}
	return repair, nil
}
