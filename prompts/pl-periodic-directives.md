# PL surfacing contract + standing periodic directives

> **Single source of truth: `template-fragments/pl-periodic-directives.template.md`.**
> This file is a pointer, not the content.

The PL surfacing contract (Tier-1 operational / Tier-2 human-decision routing,
Slack vs mayor-mail rules) and the standing periodic directives (STATUS_UPDATE,
DEEP_AUDIT, pool-saturation, …) are maintained as **one shared Go-template
fragment**, included verbatim into every rig-PL prompt:

```
template-fragments/pl-periodic-directives.template.md   (the {{ define }})
  └─ each agents/<rig>-pl/prompt.template.md renders it via {{ template "pl-periodic-directives" . }}
```

**To edit the contract:** edit `template-fragments/pl-periodic-directives.template.md`
(the markdown between its `{{ define }}` / `{{ end }}` lines). The change lands
in all 8 PL prompts on their next reset (nightly `pl-cycle`, or `gc session
reset <pl>`). Do **not** edit the per-agent `agents/<rig>-pl/prompt.template.md`
files — they no longer carry the block; they only `{{ template }}` it.

Converted from the prior hand-synced-across-8-templates model on 2026-07-03
(dr-zsxe0). The gc prompt renderer loads city-level `template-fragments/`
(`cmd/gc/prompt.go` `loadSharedTemplates`), so a city-local fragment is in scope
for every PL prompt.
