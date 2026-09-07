package sessionlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// codexTokenUsage mirrors the token-usage object the codex CLI embeds in
// event_msg token_count payloads (both total_token_usage and
// last_token_usage share this shape).
type codexTokenUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	CacheWriteInputTokens int `json:"cache_write_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
	TotalTokens           int `json:"total_tokens"`
}

type codexUsageInfo struct {
	TotalTokenUsage    codexTokenUsage `json:"total_token_usage"`
	LastTokenUsage     codexTokenUsage `json:"last_token_usage"`
	ModelContextWindow *int            `json:"model_context_window"`
}

// codexUsagePayload is the subset of event_msg and token_usage_record payloads
// needed for usage extraction. Info is null on rate-limit-only refreshes.
type codexUsagePayload struct {
	Type             string           `json:"type"`
	Model            string           `json:"model"` // turn_context payloads only
	Info             *codexUsageInfo  `json:"info"`
	Usage            *codexTokenUsage `json:"usage"`
	ThreadTokenUsage *codexTokenUsage `json:"thread_token_usage"`
}

// ExtractCodexTailMeta reads model and context metadata from the tail of a
// Codex rollout transcript. Context usage comes from the latest distinct
// event_msg token_count whose info is not null, paired with its most recent
// preceding turn_context model. Duplicate cumulative totals retain their
// first-observed model because Codex can re-emit a prior turn's final snapshot
// after the next turn_context. When the read window is truncated, its first
// positive cumulative total is kept only as an unattributable duplicate anchor;
// a later distinct total can be paired only with an in-window turn_context.
// When no attributable usage exists, the latest turn_context still supplies
// model-only metadata. Codex input_tokens already includes cached_input_tokens,
// so context occupancy uses input_tokens directly.
func ExtractCodexTailMeta(path string) (*TailMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	data, startsMidLine, truncated, err := readTailWindow(f, tailChunkSize)
	if err != nil {
		return nil, err
	}
	return extractCodexTailMetaFromLines(splitLines(data), startsMidLine, truncated), nil
}

// ExtractCodexTailMetaFromSearchPaths reads Codex tail metadata only after
// verifying path resolves under one of the merged Codex session roots (the
// defaults plus searchPaths).
func ExtractCodexTailMetaFromSearchPaths(searchPaths []string, path string) (*TailMeta, error) {
	safePath, err := validateSearchPathFile(mergeCodexSearchPaths(searchPaths), path)
	if err != nil {
		return nil, err
	}
	return ExtractCodexTailMeta(safePath)
}

func extractCodexTailMetaFromLines(lines [][]byte, startsMidLine, truncated bool) *TailMeta {
	scan := &codexTailScan{
		truncated:          truncated,
		anchorFirstTotal:   truncated,
		usageModelsByTotal: make(map[int]string),
	}
	for i := 0; i < len(lines); i++ {
		var entry codexRawEntry
		if err := json.Unmarshal(lines[i], &entry); err != nil {
			if i == len(lines)-1 && (i != 0 || !startsMidLine) {
				scan.malformedTail = true
			}
			continue
		}

		var payload codexUsagePayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			continue
		}
		if entry.Type == "turn_context" && payload.Model != "" {
			scan.latestModel = payload.Model
			continue
		}
		if entry.Type == "event_msg" && payload.Type == "token_count" && payload.Info != nil {
			scan.observeTokenCount(payload.Info)
		}
	}
	return scan.result()
}

// codexTailScan folds Codex rollout tail entries into the latest model and the
// latest attributable usage. A tail-only read keeps its first positive
// cumulative total only as an unattributable duplicate anchor; a later distinct
// total pairs only with an in-window turn_context, so usage never relabels
// another model's work.
type codexTailScan struct {
	truncated           bool
	latestModel         string
	usageModel          string
	latestUsage         *codexUsageInfo
	latestUsageTotal    int
	hasLatestUsageTotal bool
	usageModelsByTotal  map[int]string
	malformedTail       bool
	anchorFirstTotal    bool
}

// observeTokenCount folds one non-nil token_count event payload into the scan.
func (s *codexTailScan) observeTokenCount(info *codexUsageInfo) {
	total := info.TotalTokenUsage.TotalTokens
	if total <= 0 {
		s.hasLatestUsageTotal = false
		s.latestUsage = info
		s.usageModel = s.latestModel
		return
	}
	if firstModel, seen := s.usageModelsByTotal[total]; seen {
		if s.hasLatestUsageTotal && total == s.latestUsageTotal {
			s.latestUsage = info
			s.usageModel = firstModel
		}
		return
	}
	s.usageModelsByTotal[total] = s.latestModel
	if s.anchorFirstTotal {
		// A tail-only read cannot tell whether its first cumulative total is new
		// or a re-emission of a snapshot before the window. Keep it only as a
		// duplicate anchor; assigning its usage to the current turn_context could
		// relabel another model's work. A later distinct total is attributable
		// again.
		s.anchorFirstTotal = false
		s.latestUsage = nil
		s.usageModel = ""
		s.hasLatestUsageTotal = false
		return
	}
	if s.truncated && s.latestModel == "" {
		// Distinct totals after the anchor are attributable only when their
		// producing turn_context is present in the retained window. Recording the
		// empty association also prevents a later duplicate from being relabeled
		// after a model appears.
		return
	}
	s.latestUsageTotal = total
	s.hasLatestUsageTotal = true
	s.latestUsage = info
	s.usageModel = s.latestModel
}

// result assembles the TailMeta from the folded scan state, pairing usage with
// the model from the same turn and deriving bounded context occupancy.
func (s *codexTailScan) result() *TailMeta {
	model := s.latestModel
	if s.latestUsage != nil {
		// Keep usage and model from the same turn. A later turn_context may
		// select a new model before its first token_count arrives; pairing that
		// model with the prior turn's usage would produce inconsistent context.
		model = s.usageModel
	}
	if model == "" && s.latestUsage == nil && !s.malformedTail {
		return nil
	}
	result := &TailMeta{Model: model, MalformedTail: s.malformedTail}
	if s.latestUsage == nil {
		return result
	}

	contextWindow := 0
	if s.latestUsage.ModelContextWindow != nil {
		contextWindow = *s.latestUsage.ModelContextWindow
	} else {
		contextWindow = ModelContextWindow(model)
	}
	if contextWindow <= 0 {
		return result
	}

	inputTokens := s.latestUsage.LastTokenUsage.InputTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	result.ContextUsage = &ContextUsage{
		InputTokens:   inputTokens,
		Percentage:    boundedContextPercentage(inputTokens, contextWindow),
		ContextWindow: contextWindow,
	}
	return result
}

func boundedContextPercentage(inputTokens, contextWindow int) int {
	if inputTokens <= 0 || contextWindow <= 0 {
		return 0
	}
	if inputTokens >= contextWindow {
		return 100
	}

	// Find floor(inputTokens*100/contextWindow) without multiplying the
	// untrusted token count. ceil(pct*contextWindow/100) is the smallest input
	// that earns pct; splitting the window first keeps every product in range.
	windowHundreds := contextWindow / 100
	windowRemainder := contextWindow % 100
	for percentage := 99; percentage > 0; percentage-- {
		threshold := windowHundreds * percentage
		remainderProduct := windowRemainder * percentage
		threshold += remainderProduct / 100
		if remainderProduct%100 != 0 {
			threshold++
		}
		if inputTokens >= threshold {
			return percentage
		}
	}
	return 0
}

// ExtractCodexTailUsage reads the tail of a codex rollout transcript and
// returns one usage-bearing TailUsage per API call, in file order. The codex
// CLI writes an event_msg token_count line after each API call within a
// turn: last_token_usage is the per-call usage, total_token_usage is the
// strictly increasing session cumulative. Mapping (verified against real
// rollouts, where total_tokens = input_tokens + output_tokens):
//
//   - InputTokens = last input_tokens - cached_input_tokens (cached input is
//     a subset of input; clamped at zero)
//   - CacheReadTokens = last cached_input_tokens
//   - OutputTokens = last output_tokens (reasoning_output_tokens is a subset
//     of output_tokens and must not be added)
//   - ReasoningTokens = last reasoning_output_tokens
//   - CacheCreationTokens = cache_write_input_tokens when the newer
//     token_usage_record shape supplies it, otherwise zero
//
// Model comes from the latest preceding turn_context payload.model, including
// one before the retained tail window (usage records carry no model).
// MessageID is the cumulative-total identity
// ("total:<total_tokens>") so the exact-duplicate token_count emissions the
// CLI produces collapse to a single entry (the last observed wins, except a
// first-observed non-empty Model is kept — a duplicate re-emitted after a
// model-switching turn_context must not relabel the invocation), and
// EntryUUID is the line timestamp. token_count lines with null info
// (rate-limit-only refreshes) and all-zero per-call usage are skipped;
// malformed lines are tolerated silently. The scan window is the last
// tailChunkSize bytes, so usage that scrolled past the window is not
// returned.
func ExtractCodexTailUsage(path string) ([]TailUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	data, _, truncated, err := readTailWindow(f, tailChunkSize)
	if err != nil {
		return nil, err
	}
	var turnModel string
	if truncated {
		size, seekErr := f.Seek(0, io.SeekEnd)
		if seekErr != nil {
			return nil, seekErr
		}
		turnModel, err = latestCodexModelBefore(f, size-int64(len(data)))
		if err != nil {
			return nil, err
		}
	}

	var usages []TailUsage
	// byMessageID maps a cumulative-total identity to its index in usages so
	// duplicate token_count emissions collapse to a single entry.
	byMessageID := make(map[string]int)
	for _, line := range splitLines(data) {
		var entry codexRawEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		var payload codexUsagePayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			continue
		}
		if entry.Type == "turn_context" {
			if payload.Model != "" {
				turnModel = payload.Model
			}
			continue
		}
		u, ok := codexUsageFromPayload(entry, payload, turnModel)
		if !ok {
			continue
		}
		if i, seen := byMessageID[u.MessageID]; seen {
			// The CLI re-emits the prior turn's final cumulative snapshot
			// after a new turn_context; the first-observed model is the one
			// that produced the invocation, so the collapse refreshes the
			// rest of the entry but never relabels a non-empty model.
			if usages[i].Model != "" {
				u.Model = usages[i].Model
			}
			usages[i] = u
			continue
		}
		byMessageID[u.MessageID] = len(usages)
		usages = append(usages, u)
	}
	return usages, nil
}

// ExtractCodexUsageSince returns every distinct invocation after the last
// occurrence of cursor. It takes the bounded tail fast path when the cursor is
// still there; otherwise it streams the complete rollout so a terminal sweep
// can close gaps that exceeded the live window. If a non-empty cursor no longer
// exists even in the full file, the bounded tail is returned instead of
// replaying the whole session. Duplicate legacy/new snapshots sharing a
// cumulative total collapse to one invocation. A torn final JSON object is
// ignored; any earlier malformed object is returned as an error instead of
// being represented as zero usage. For a well-formed, correctly correlated
// rollout, the terminal total has zero-token tolerance against Codex's final
// cumulative thread total.
func ExtractCodexUsageSince(path, cursor string) ([]TailUsage, error) {
	tail, err := ExtractCodexTailUsage(path)
	if err != nil {
		return nil, err
	}
	if cursor != "" {
		for i := len(tail) - 1; i >= 0; i-- {
			if tail[i].MessageID == cursor {
				return tail[i+1:], nil
			}
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	decoder := json.NewDecoder(f)
	var usages []TailUsage
	byMessageID := make(map[string]int)
	var turnModel string
	cursorFound := cursor == ""
	for {
		var entry codexRawEntry
		if err := decoder.Decode(&entry); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, fmt.Errorf("decode codex rollout: %w", err)
		}
		var payload codexUsagePayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			continue
		}
		if entry.Type == "turn_context" {
			if payload.Model != "" {
				turnModel = payload.Model
			}
			continue
		}
		u, ok := codexUsageFromPayload(entry, payload, turnModel)
		if !ok {
			continue
		}
		if cursor != "" && u.MessageID == cursor {
			// Reset on every duplicate occurrence so only entries after the last
			// copy of the already-recorded cumulative snapshot are returned.
			usages = nil
			byMessageID = make(map[string]int)
			cursorFound = true
			continue
		}
		if i, seen := byMessageID[u.MessageID]; seen {
			if usages[i].Model != "" {
				u.Model = usages[i].Model
			}
			usages[i] = u
			continue
		}
		byMessageID[u.MessageID] = len(usages)
		usages = append(usages, u)
	}
	if !cursorFound {
		return tail, nil
	}
	return usages, nil
}

// ExtractCodexUsageSinceFromSearchPaths validates a rollout path against the
// merged Codex roots before streaming usage after cursor.
func ExtractCodexUsageSinceFromSearchPaths(searchPaths []string, path, cursor string) ([]TailUsage, error) {
	safePath, err := validateSearchPathFile(mergeCodexSearchPaths(searchPaths), path)
	if err != nil {
		return nil, err
	}
	return ExtractCodexUsageSince(safePath, cursor)
}

func codexUsageFromPayload(entry codexRawEntry, payload codexUsagePayload, turnModel string) (TailUsage, bool) {
	var last codexTokenUsage
	var total, contextWindowTokens int
	switch {
	case entry.Type == "event_msg" && payload.Type == "token_count" && payload.Info != nil:
		last = payload.Info.LastTokenUsage
		total = payload.Info.TotalTokenUsage.TotalTokens
		if payload.Info.ModelContextWindow != nil {
			contextWindowTokens = *payload.Info.ModelContextWindow
		}
	case entry.Type == "token_usage_record" && payload.Usage != nil && payload.ThreadTokenUsage != nil:
		last = *payload.Usage
		total = payload.ThreadTokenUsage.TotalTokens
	default:
		return TailUsage{}, false
	}
	input := last.InputTokens - last.CachedInputTokens
	if input < 0 {
		input = 0
	}
	u := TailUsage{
		EntryUUID:           entry.Timestamp,
		MessageID:           fmt.Sprintf("total:%d", total),
		Model:               turnModel,
		InputTokens:         input,
		OutputTokens:        last.OutputTokens,
		ReasoningTokens:     last.ReasoningOutputTokens,
		CacheReadTokens:     last.CachedInputTokens,
		CacheCreationTokens: last.CacheWriteInputTokens,
		ContextWindowTokens: contextWindowTokens,
	}
	if u.InputTokens <= 0 && u.OutputTokens <= 0 && u.ReasoningTokens <= 0 && u.CacheReadTokens <= 0 && u.CacheCreationTokens <= 0 {
		return TailUsage{}, false
	}
	return u, true
}

// latestCodexModelBefore searches backward for the last complete turn_context
// before end. It reads fixed-size windows and then only the matching JSONL
// line, so a large rollout does not need to be decoded from the beginning just
// to seed the model for a tail window.
func latestCodexModelBefore(f *os.File, end int64) (string, error) {
	const searchChunk = int64(64 * 1024)
	needle := []byte(`"type":"turn_context"`)
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	fileEnd := info.Size()
	windowEnd := end
	for windowEnd > 0 {
		start := windowEnd - searchChunk
		if start < 0 {
			start = 0
		}
		readEnd := windowEnd + int64(len(needle)-1)
		if readEnd > end {
			readEnd = end
		}
		buf := make([]byte, readEnd-start)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return "", err
		}
		for search := len(buf); search > 0; {
			i := bytes.LastIndex(buf[:search], needle)
			if i < 0 {
				break
			}
			absolute := start + int64(i)
			// The forward overlap exists only to catch a needle split across a
			// chunk boundary. Candidates owned by the newer chunk were already
			// considered.
			if absolute < windowEnd {
				line, err := readJSONLLineContaining(f, absolute, fileEnd)
				if err != nil {
					return "", err
				}
				var entry codexRawEntry
				if json.Unmarshal(line, &entry) == nil && entry.Type == "turn_context" {
					var payload codexUsagePayload
					if json.Unmarshal(entry.Payload, &payload) == nil && payload.Model != "" {
						return payload.Model, nil
					}
				}
			}
			search = i
		}
		windowEnd = start
	}
	return "", nil
}

func readJSONLLineContaining(f *os.File, at, limit int64) ([]byte, error) {
	start := at
	for start > 0 {
		chunkStart := start - 4096
		if chunkStart < 0 {
			chunkStart = 0
		}
		buf := make([]byte, start-chunkStart)
		if _, err := f.ReadAt(buf, chunkStart); err != nil && err != io.EOF {
			return nil, err
		}
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			start = chunkStart + int64(i) + 1
			break
		}
		start = chunkStart
	}

	end := at
	for end < limit {
		chunkEnd := end + 4096
		if chunkEnd > limit {
			chunkEnd = limit
		}
		buf := make([]byte, chunkEnd-end)
		if _, err := f.ReadAt(buf, end); err != nil && err != io.EOF {
			return nil, err
		}
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			end += int64(i)
			break
		}
		end = chunkEnd
	}
	line := make([]byte, end-start)
	if _, err := f.ReadAt(line, start); err != nil && err != io.EOF {
		return nil, err
	}
	return line, nil
}

// ExtractCodexTailUsageFromSearchPaths reads codex tail usage only after
// verifying path resolves under one of the merged codex session roots (the
// defaults plus searchPaths). Mirrors ExtractTailUsageFromSearchPaths.
func ExtractCodexTailUsageFromSearchPaths(searchPaths []string, path string) ([]TailUsage, error) {
	safePath, err := validateSearchPathFile(mergeCodexSearchPaths(searchPaths), path)
	if err != nil {
		return nil, err
	}
	return ExtractCodexTailUsage(safePath)
}
