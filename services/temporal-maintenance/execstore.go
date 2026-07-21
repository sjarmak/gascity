package temporalmaintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ExecStatus is the lifecycle of one persisted side-effect record.
type ExecStatus string

const (
	// ExecPending: the key was claimed and the command was about to run. A
	// record left in this state means the worker crashed mid-execution — the
	// command may or may not have taken effect, so it must NOT be re-run.
	ExecPending ExecStatus = "pending"
	// ExecDone: the command ran and succeeded exactly once.
	ExecDone ExecStatus = "done"
	// ExecFailed: the command ran and returned an error. Terminal — re-running
	// could double-execute a partially-applied side effect, so it never retries.
	ExecFailed ExecStatus = "failed"
)

// ExecRecord is the on-disk record for one idempotency key. It carries only the
// history-safe ProposedMutation plus execution bookkeeping.
type ExecRecord struct {
	Key         string           `json:"key"`
	Mutation    ProposedMutation `json:"mutation"`
	Status      ExecStatus       `json:"status"`
	ResultRef   string           `json:"result_ref,omitempty"`
	Err         string           `json:"err,omitempty"`
	ClaimedAt   time.Time        `json:"claimed_at"`
	CompletedAt time.Time        `json:"completed_at,omitempty"`
}

// KeyStore is a crash-safe, at-most-once side-effect ledger on disk. One file
// per idempotency key. The claim is atomic (exclusive hard-link of a fully
// written temp file), so two callers — or a worker and its restarted successor —
// can never both believe they own execution of the same key.
//
// The safety direction is deliberate: a crash between claim and completion
// leaves a pending record, and a later Claim of that key reports "already
// claimed" rather than re-running. At-most-once beats at-least-once for external
// mutations; the P3 lost-boundary reconciler repairs the rare stuck-pending key.
type KeyStore struct {
	dir string
	mu  sync.Mutex // serializes in-process claims/writes; cross-process safety is the exclusive link
}

// NewKeyStore roots a store at dir, creating it (0700) if absent.
func NewKeyStore(dir string) (*KeyStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("KeyStore: dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("KeyStore: create %s: %w", dir, err)
	}
	return &KeyStore{dir: dir}, nil
}

func (s *KeyStore) file(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

func loadRecord(path string) (ExecRecord, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ExecRecord{}, false, nil
		}
		return ExecRecord{}, false, err
	}
	var rec ExecRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		// A key file is only ever created by an exclusive Link of a fully written,
		// synced temp file, or replaced by an atomic Rename — so a process crash
		// can never leave it half-written. A present-but-unparseable file is real
		// corruption or a schema mismatch: surface it rather than fabricating a
		// pending record that hides the problem.
		return ExecRecord{}, true, fmt.Errorf("KeyStore: corrupt record %s: %w", path, err)
	}
	return rec, true, nil
}

// fsyncDir flushes a directory entry (the link/rename itself, not just file
// contents) so a claim or completion survives a host crash, not only a process
// crash.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Load returns the record for key, if any.
func (s *KeyStore) Load(key string) (ExecRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return loadRecord(s.file(key))
}

// Claim atomically reserves execution of m's key. It returns claimedNow=true to
// exactly one caller (which then runs the command and calls Complete/Fail);
// every other caller gets claimedNow=false and the existing record.
func (s *KeyStore) Claim(m ProposedMutation) (ExecRecord, bool, error) {
	if m.IdempotencyKey == "" {
		return ExecRecord{}, false, fmt.Errorf("KeyStore.Claim: mutation has no idempotency key")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.file(m.IdempotencyKey)
	if rec, ok, err := loadRecord(path); err != nil || ok {
		return rec, false, err
	}

	rec := ExecRecord{
		Key:       m.IdempotencyKey,
		Mutation:  m,
		Status:    ExecPending,
		ClaimedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return ExecRecord{}, false, err
	}

	tmp, err := os.CreateTemp(s.dir, ".claim-*")
	if err != nil {
		return ExecRecord{}, false, err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return ExecRecord{}, false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return ExecRecord{}, false, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return ExecRecord{}, false, err
	}

	// Exclusive link is the atomic claim: it fails if the key file already
	// exists, closing the race between two claimers of the same key.
	if err := os.Link(tmpName, path); err != nil {
		os.Remove(tmpName)
		if os.IsExist(err) {
			existing, ok, lerr := loadRecord(path)
			return existing, false, wrapClaimLoss(ok, lerr)
		}
		return ExecRecord{}, false, err
	}
	os.Remove(tmpName)
	// Persist the directory entry before reporting ownership: without this a host
	// crash could lose the claim after the command ran, letting a retry re-run it.
	// If the entry can't be durably recorded we refuse the claim — the key file
	// still exists as pending, so a retry finds it and declines to re-run.
	if err := fsyncDir(s.dir); err != nil {
		return ExecRecord{}, false, fmt.Errorf("KeyStore.Claim: fsync dir after claim: %w", err)
	}
	return rec, true, nil
}

func wrapClaimLoss(ok bool, err error) error {
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("KeyStore.Claim: lost claim race but key file vanished")
	}
	return nil
}

// Complete marks a pending key done with an optional result reference.
func (s *KeyStore) Complete(key, resultRef string) error {
	return s.finish(key, ExecDone, resultRef, "")
}

// Fail marks a pending key failed. resultRef carries any partial result (e.g. a
// bead created before a later step failed) so the reconciler reads a structured
// ref rather than parsing errMsg.
func (s *KeyStore) Fail(key, resultRef, errMsg string) error {
	return s.finish(key, ExecFailed, resultRef, errMsg)
}

func (s *KeyStore) finish(key string, status ExecStatus, resultRef, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.file(key)
	rec, ok, err := loadRecord(path)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("KeyStore.finish: no claim for key %q", key)
	}
	// Only a pending claim may be finished. A double-finish or a finish of an
	// already-terminal key is a caller bug — refuse it rather than silently
	// overwriting a recorded outcome.
	if rec.Status != ExecPending {
		return fmt.Errorf("KeyStore.finish: key %q is already %s, refusing to overwrite", key, rec.Status)
	}
	rec.Status = status
	rec.ResultRef = resultRef
	rec.Err = errMsg
	rec.CompletedAt = time.Now().UTC()
	return s.writeTerminalLocked(path, rec)
}

// Quarantine transitions a poisoned pending claim — one whose worker crashed
// between Claim and Complete/Fail — to failed with a reason, so the drop is a
// durable, observable record instead of a permanent pending tombstone. It
// preserves at-most-once: quarantining never runs the command, and the failed
// record stays terminal (a later Propose still refuses it), so a redelivery
// cannot re-execute the mutation.
//
// It is idempotent for repeated redeliveries: on a record that is already
// terminal (a prior quarantine, or a genuine command failure) it returns the
// existing record with transitioned=false and does NOT overwrite the reason, so
// callers escalate exactly once. An unknown key is an error, not a silent no-op.
func (s *KeyStore) Quarantine(key, reason string) (ExecRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.file(key)
	rec, ok, err := loadRecord(path)
	if err != nil {
		return ExecRecord{}, false, err
	}
	if !ok {
		return ExecRecord{}, false, fmt.Errorf("KeyStore.Quarantine: no claim for key %q", key)
	}
	if rec.Status != ExecPending {
		// Already terminal — nothing to quarantine, and the existing outcome
		// (and its reason) must not be overwritten by a duplicate redelivery.
		return rec, false, nil
	}
	rec.Status = ExecFailed
	rec.Err = reason
	rec.CompletedAt = time.Now().UTC()
	// ResultRef is left untouched: a crash leaves it empty, but a claim that
	// carried a partial ref keeps it so the orphan is still structurally
	// recorded rather than erased.
	if err := s.writeTerminalLocked(path, rec); err != nil {
		return ExecRecord{}, false, err
	}
	return rec, true, nil
}

// writeTerminalLocked atomically replaces the on-disk record for path with rec
// (temp write + fsync + rename + dir fsync), so the terminal outcome is never
// observed half-written and survives a host crash. A lost rename reverts to the
// prior record, which is fail-safe (a pending record is never re-run). The
// caller must hold s.mu.
func (s *KeyStore) writeTerminalLocked(path string, rec ExecRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".finish-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	// Rename replaces atomically — the record is never observed half-written.
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := fsyncDir(s.dir); err != nil {
		return fmt.Errorf("KeyStore: fsync dir after rename: %w", err)
	}
	return nil
}

// All returns every record, ordered by claim time then key, for a deterministic
// snapshot (mirrors DryRunAdapter.Recorded's stable ordering).
func (s *KeyStore) All() ([]ExecRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []ExecRecord
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		rec, ok, err := loadRecord(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ClaimedAt.Equal(out[j].ClaimedAt) {
			return out[i].Key < out[j].Key
		}
		return out[i].ClaimedAt.Before(out[j].ClaimedAt)
	})
	return out, nil
}
