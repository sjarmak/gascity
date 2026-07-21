{{ define "rollup-shape" -}}

Every tick produces zero or more **rollup beads** with this exact label
set:

- `rollup` (always)
- `rig:<your-rig>` (always)
- `severity:escalate` OR `severity:info` (always exactly one)
- `ref:<source-bead-id>` (for each source bead the rollup is about)

`severity:escalate` means: this needs the human now. The downstream order
will deliver it. Use sparingly — once delivered, the human is paged.

`severity:info` means: this is for the audit trail / weekly digest. Not
delivered. Use freely.

Bead title format:

```
Rollup(<your-rig>): <one-line summary in your project's voice>
```

Bead description must be exactly this template, filled in:

```
Rig: <your-rig>
Project: <name from brief>
State: <one line — "healthy", "blocked on X", "needs decision on Y">
Source bead(s): <comma-separated ids>
Stuck since: <ISO 8601 timestamp of earliest source bead's relevant transition>
Why: <one paragraph in your persona's voice — what is happening, why it matters, framed per your brief's reporting lens>
Smallest ask: <single concrete decision or question the human can answer in under a minute, or "none — informational">
```

The downstream delivery pipeline parses this format. Drift from the
template and your rollup will not be deliverable.

### Slack-mrkdwn for any prose you write into the bead body

Rollup-bead bodies are posted to Slack verbatim by the downstream delivery
pipeline. Slack uses **single-asterisk bold** (`*bold*`), NOT
GitHub-markdown double-asterisk (`**bold**`). Same for italics: underscores
(`_italic_`). Tables go in code fences. Links are `<url|label>` form, not
`[label](url)`.

Use the Stephanie-facing executive-skimmable shape inside the `Why:` field
when applicable:

```
*TL;DR:* 1-2 sentences.

*Context (≤3 bullets, OPTIONAL):* only if TL;DR isn't enough.

*Asks:* "none — informational" OR a numbered list, each with: what to
decide / paths available / recommended path + why / why YOUR call.
```
{{- end }}
