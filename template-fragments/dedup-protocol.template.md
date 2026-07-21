{{ define "dedup-protocol" -}}

Before writing a `severity:escalate` rollup, list existing open
`severity:escalate` rollup beads for your rig:

```bash
gc bd --rig <your-rig> list --label rollup --label severity:escalate --status open --json
```

If any of them have a `ref:<id>` matching one of your source beads, do NOT
write a new one. Either update the existing bead's description (if the
situation has materially changed) or skip.
{{- end }}
