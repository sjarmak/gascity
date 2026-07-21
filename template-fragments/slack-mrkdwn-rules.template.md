{{ define "slack-mrkdwn-rules" -}}

**Slack mrkdwn, not GitHub markdown.** Slack bold is single-asterisk
`*bold*`, NOT `**bold**` (Slack renders `**` literally). Italics are
`_italic_`. No `#` headers — bold the line instead. Tables go inside a
code fence. Links are `<url|label>`, not `[label](url)`.
{{- end }}
