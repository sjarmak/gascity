package api

import (
	"github.com/gastownhall/gascity/internal/mail/beadmail"
	"github.com/gastownhall/gascity/internal/maildelivery"
)

// MailDurableSendInput is the stable notify-only send request.
type MailDurableSendInput struct {
	CityScope
	Body struct {
		StableKey        string   `json:"stable_key" minLength:"1"`
		Recipient        string   `json:"recipient" minLength:"1"`
		SenderCandidates []string `json:"sender_candidates" minItems:"1"`
		Subject          string   `json:"subject,omitempty"`
		Message          string   `json:"message" minLength:"1"`
	}
}

// MailDurableSendOutput is the typed durable send response.
type MailDurableSendOutput struct {
	Body MailDurableSendBody
}

// MailDurableSendBody preserves a durable result on result-bearing failure.
type MailDurableSendBody struct {
	OK          bool                       `json:"ok"`
	FailureCode string                     `json:"failure_code,omitempty"`
	Result      beadmail.DurableSendResult `json:"result"`
}

// MailDeliveryReconcileInput is one bounded seat reconciliation request.
type MailDeliveryReconcileInput struct {
	CityScope
	Body struct {
		SeatRef            string `json:"seat_ref" minLength:"1"`
		Limit              int    `json:"limit" minimum:"1" maximum:"100"`
		ExpectedDeliveryID string `json:"expected_delivery_id,omitempty"`
	}
}

// MailDeliveryReconcileOutput is the typed reconciliation response.
type MailDeliveryReconcileOutput struct {
	Body MailDeliveryReconcileBody
}

// MailDeliveryReconcileBody preserves a partial report on result-bearing failure.
type MailDeliveryReconcileBody struct {
	OK          bool                         `json:"ok"`
	FailureCode string                       `json:"failure_code,omitempty"`
	Report      maildelivery.ReconcileReport `json:"report"`
}

// MailDeliveryAttemptInput identifies one canonical transport attempt.
type MailDeliveryAttemptInput struct {
	CityScope
	AttemptID string `path:"attemptID" minLength:"1"`
}

// MailDeliveryAttemptOutput is the typed invoke or status response.
type MailDeliveryAttemptOutput struct {
	Body MailDeliveryAttemptBody
}

// MailDeliveryAttemptBody preserves an attempt on result-bearing failure.
type MailDeliveryAttemptBody struct {
	OK          bool                          `json:"ok"`
	FailureCode string                        `json:"failure_code,omitempty"`
	Attempt     maildelivery.TransportAttempt `json:"attempt"`
}
