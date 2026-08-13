package maildelivery

import (
	"fmt"
	"sort"
	"time"
)

// DeliveryKey is the immutable canonical scan order.
type DeliveryKey struct {
	CreatedAt  time.Time `json:"created_at"`
	DeliveryID string    `json:"delivery_id"`
}

// IsZero reports whether the key is the sweep origin.
func (k DeliveryKey) IsZero() bool { return k.CreatedAt.IsZero() && k.DeliveryID == "" }

// SweepCheckpoint is the CAS-protected durable per-seat scan cursor.
type SweepCheckpoint struct {
	Version       int         `json:"version"`
	SeatRef       string      `json:"seat_ref"`
	Generation    uint64      `json:"generation"`
	HighWatermark DeliveryKey `json:"high_watermark"`
	After         DeliveryKey `json:"after"`
	Revision      uint64      `json:"-"`
}

// SweepPlan is one bounded, deterministic page and whether it closes the sweep.
type SweepPlan struct {
	HighWatermark DeliveryKey   `json:"high_watermark"`
	Page          []DeliveryKey `json:"page"`
	Wrap          bool          `json:"wrap"`
}

// PlanSweep selects a page bounded by the sweep's captured high-watermark.
func PlanSweep(cp SweepCheckpoint, available []DeliveryKey, pageSize int) (SweepPlan, error) {
	if err := cp.validate(); err != nil {
		return SweepPlan{}, err
	}
	if cp.HighWatermark.IsZero() {
		return SweepPlan{}, fmt.Errorf("mail delivery sweep high-watermark is not captured")
	}
	if pageSize <= 0 {
		return SweepPlan{}, fmt.Errorf("mail delivery sweep page size must be positive")
	}
	if !sort.SliceIsSorted(available, func(i, j int) bool { return compareKey(available[i], available[j]) < 0 }) {
		return SweepPlan{}, fmt.Errorf("mail delivery sweep input is not strictly sorted")
	}
	for i, key := range available {
		if err := key.validate(); err != nil || (i > 0 && compareKey(available[i-1], key) == 0) {
			return SweepPlan{}, fmt.Errorf("mail delivery sweep key is invalid")
		}
	}

	high := cp.HighWatermark
	page := make([]DeliveryKey, 0, pageSize)
	for _, key := range available {
		if compareKey(key, cp.After) <= 0 || (!high.IsZero() && compareKey(key, high) > 0) {
			continue
		}
		page = append(page, key)
		if len(page) == pageSize {
			break
		}
	}
	wrap := len(page) > 0 && compareKey(page[len(page)-1], high) == 0
	if len(page) == 0 && !high.IsZero() {
		wrap = true
	}
	return SweepPlan{HighWatermark: high, Page: page, Wrap: wrap}, nil
}

// AdvanceSweep returns the next checkpoint after durable processing.
func AdvanceSweep(cp SweepCheckpoint, plan SweepPlan) (SweepCheckpoint, error) {
	if err := cp.validate(); err != nil {
		return SweepCheckpoint{}, err
	}
	if cp.HighWatermark.IsZero() || cp.HighWatermark != plan.HighWatermark {
		return SweepCheckpoint{}, fmt.Errorf("mail delivery sweep high-watermark changed")
	}
	if len(plan.Page) > 0 {
		cp.After = plan.Page[len(plan.Page)-1]
	}
	cp.Revision++
	if plan.Wrap {
		cp.Generation++
		cp.HighWatermark = DeliveryKey{}
		cp.After = DeliveryKey{}
	}
	return cp, nil
}

func (cp SweepCheckpoint) validate() error {
	if cp.Version != 1 || cp.Generation == 0 || cp.Revision == 0 || !validRef(cp.SeatRef) {
		return fmt.Errorf("mail delivery sweep checkpoint is invalid")
	}
	if cp.HighWatermark.IsZero() && !cp.After.IsZero() {
		return fmt.Errorf("mail delivery sweep cursor has no high-watermark")
	}
	if !cp.HighWatermark.IsZero() {
		if cp.HighWatermark.validate() != nil || (!cp.After.IsZero() && cp.After.validate() != nil) || compareKey(cp.After, cp.HighWatermark) > 0 {
			return fmt.Errorf("mail delivery sweep key range is invalid")
		}
	}
	return nil
}

func (k DeliveryKey) validate() error {
	if k.CreatedAt.IsZero() || k.CreatedAt.Location() != time.UTC || !validRef(k.DeliveryID) {
		return fmt.Errorf("mail delivery key is invalid")
	}
	return nil
}

func compareKey(a, b DeliveryKey) int {
	if a.IsZero() {
		if b.IsZero() {
			return 0
		}
		return -1
	}
	if b.IsZero() {
		return 1
	}
	if cmp := a.CreatedAt.Compare(b.CreatedAt); cmp != 0 {
		return cmp
	}
	if a.DeliveryID < b.DeliveryID {
		return -1
	}
	if a.DeliveryID > b.DeliveryID {
		return 1
	}
	return 0
}
