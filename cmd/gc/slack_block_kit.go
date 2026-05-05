package main

import (
	"errors"
	"fmt"
	"strings"
)

// Block Kit renderers for the `gc slack post-message` agent-driven status
// projection surface. These functions are pure: a typed payload in, a
// []slackBlock out. They never call out to Slack — the CLI runner does
// that. Keeping render and transport separate lets tests assert on the
// block structure without httptest.
//
// See https://api.slack.com/block-kit for the schema. We model only the
// subset required by milestone / progress / rollup payloads.

// slackStatusKind names a renderer. The set is closed: each kind has a
// dedicated builder. New kinds are added as code, not config — the
// rendering shape is part of the SDK surface, not user config.
type slackStatusKind string

const (
	slackStatusKindMilestone slackStatusKind = "milestone"
	slackStatusKindProgress  slackStatusKind = "progress"
	slackStatusKindRollup    slackStatusKind = "rollup"
)

// slackStatusPayload is the typed input every renderer accepts. Fields
// are optional unless the renderer requires them; renderers fail fast
// when required fields are missing. Title is required for all kinds —
// Block Kit headers are the visual anchor for status posts.
type slackStatusPayload struct {
	Title    string               `json:"title"`
	Summary  string               `json:"summary,omitempty"`
	Fields   []slackStatusField   `json:"fields,omitempty"`
	Items    []slackStatusItem    `json:"items,omitempty"`
	Progress *slackStatusProgress `json:"progress,omitempty"`
	Footer   string               `json:"footer,omitempty"`
}

// slackStatusField is a label/value pair rendered as a Block Kit
// section field. Both sides are mrkdwn. Used by milestone payloads.
type slackStatusField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// slackStatusItem is a labelled list entry for rollup payloads. Items
// are rendered as bulleted mrkdwn lines.
type slackStatusItem struct {
	Label string `json:"label"`
	Value string `json:"value,omitempty"`
}

// slackStatusProgress carries the discrete progress fraction rendered
// into a unicode bar. Total must be > 0 and Current must be in
// [0, Total]; the renderer validates this.
type slackStatusProgress struct {
	Current int `json:"current"`
	Total   int `json:"total"`
}

// slackBlock is a single Block Kit element. The exported field set is
// minimal — we only emit blocks we actually use (header, section,
// context, divider). The omitempty tags keep the marshaled JSON
// idiomatic for Slack.
type slackBlock struct {
	Type     string           `json:"type"`
	Text     *slackBlockText  `json:"text,omitempty"`
	Fields   []slackBlockText `json:"fields,omitempty"`
	Elements []slackBlockText `json:"elements,omitempty"`
}

// slackBlockText is a Block Kit text object. Type is "plain_text" or
// "mrkdwn"; Slack rejects anything else for these fields.
type slackBlockText struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Emoji *bool  `json:"emoji,omitempty"`
}

// renderStatusBlocks dispatches to the renderer for kind. The returned
// slice is safe to marshal directly into Slack chat.postMessage's
// `blocks` parameter.
func renderStatusBlocks(kind slackStatusKind, p slackStatusPayload) ([]slackBlock, error) {
	if strings.TrimSpace(p.Title) == "" {
		return nil, errors.New("title is required")
	}
	switch kind {
	case slackStatusKindMilestone:
		return renderMilestoneBlocks(p), nil
	case slackStatusKindProgress:
		return renderProgressBlocks(p)
	case slackStatusKindRollup:
		return renderRollupBlocks(p), nil
	default:
		return nil, fmt.Errorf("unknown slack status kind %q (want milestone|progress|rollup)", kind)
	}
}

func renderMilestoneBlocks(p slackStatusPayload) []slackBlock {
	blocks := []slackBlock{headerBlock(p.Title)}
	if p.Summary != "" {
		blocks = append(blocks, mrkdwnSection(p.Summary))
	}
	if len(p.Fields) > 0 {
		fields := make([]slackBlockText, 0, len(p.Fields))
		for _, f := range p.Fields {
			fields = append(fields, slackBlockText{
				Type: "mrkdwn",
				Text: fmt.Sprintf("*%s*\n%s", f.Label, f.Value),
			})
		}
		blocks = append(blocks, slackBlock{Type: "section", Fields: fields})
	}
	if p.Footer != "" {
		blocks = append(blocks, contextBlock(p.Footer))
	}
	return blocks
}

func renderProgressBlocks(p slackStatusPayload) ([]slackBlock, error) {
	if p.Progress == nil {
		return nil, errors.New("progress kind requires a progress object")
	}
	if p.Progress.Total <= 0 {
		return nil, fmt.Errorf("progress.total must be > 0, got %d", p.Progress.Total)
	}
	if p.Progress.Current < 0 || p.Progress.Current > p.Progress.Total {
		return nil, fmt.Errorf("progress.current must be in [0, %d], got %d", p.Progress.Total, p.Progress.Current)
	}
	bar := progressBar(p.Progress.Current, p.Progress.Total)
	body := fmt.Sprintf("%s  %d/%d", bar, p.Progress.Current, p.Progress.Total)
	if p.Summary != "" {
		body = p.Summary + "\n" + body
	}
	blocks := []slackBlock{headerBlock(p.Title), mrkdwnSection(body)}
	if p.Footer != "" {
		blocks = append(blocks, contextBlock(p.Footer))
	}
	return blocks, nil
}

func renderRollupBlocks(p slackStatusPayload) []slackBlock {
	blocks := []slackBlock{headerBlock(p.Title)}
	if p.Summary != "" {
		blocks = append(blocks, mrkdwnSection(p.Summary))
	}
	if len(p.Items) > 0 {
		var b strings.Builder
		for i, it := range p.Items {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString("• *")
			b.WriteString(it.Label)
			b.WriteString("*")
			if it.Value != "" {
				b.WriteString(": ")
				b.WriteString(it.Value)
			}
		}
		blocks = append(blocks, mrkdwnSection(b.String()))
	}
	if p.Footer != "" {
		blocks = append(blocks, contextBlock(p.Footer))
	}
	return blocks
}

// headerBlock returns a Block Kit header block. Slack truncates header
// text at 150 chars; we leave that to Slack rather than silently
// trimming here — clearer error from the API than a guessed cut.
func headerBlock(text string) slackBlock {
	emoji := true
	return slackBlock{
		Type: "header",
		Text: &slackBlockText{
			Type:  "plain_text",
			Text:  text,
			Emoji: &emoji,
		},
	}
}

// mrkdwnSection returns a section block whose body is mrkdwn.
func mrkdwnSection(text string) slackBlock {
	return slackBlock{
		Type: "section",
		Text: &slackBlockText{Type: "mrkdwn", Text: text},
	}
}

// contextBlock returns a Block Kit context block with a single mrkdwn
// element — used for footers/timestamps that should render small.
func contextBlock(text string) slackBlock {
	return slackBlock{
		Type:     "context",
		Elements: []slackBlockText{{Type: "mrkdwn", Text: text}},
	}
}

// progressBar renders a 20-cell unicode bar for current/total. Filled
// cells are █, empty cells are ░. The width is fixed at 20 — wide
// enough to read on mobile, narrow enough to fit alongside the
// fraction without wrapping.
func progressBar(current, total int) string {
	const width = 20
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	filled := current * width / total
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}
