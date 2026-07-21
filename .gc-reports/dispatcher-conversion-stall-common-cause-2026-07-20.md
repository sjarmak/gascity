# Dispatcher conversion-stall common-cause investigation — 2026-07-20

## Outcome

The seven 01:17–01:19 EDT resets were not seven independently established
dispatcher wedges. `bin/dispatcher-watchdog` computes one city-wide pending set
for every dispatcher (`control_pending_set`, lines 55–82), then stores that same
set under each session ID (`conversion_stalled`, lines 94–119). At the kill
run, all seven entries contained the identical 66 IDs, identical
`since=2026-07-20T03:33:13Z`, and identical SHA-256 prefix
`8eb9e43e0424bb3e`. The set included unrelated rig prefixes including `aoa`,
`code-intel`, `codeprobe`, `EnterpriseBench`, `gascity-dashboard`, `gc`, `gpk`,
and `mem`.

This falsifies the watchdog's per-dispatcher attribution. A bead stuck for any
reason anywhere in the city can hold every dispatcher's clock stable and cause
all active dispatchers to be killed serially. The existing comments explicitly
describe the set as city-wide (lines 55–59), while the kill loop applies it to
each active dispatcher independently (lines 151–190).

## Seven reset map

The watchdog state age is measured from 23:33:13 EDT on 2026-07-19. Replacement
IDs are the first different same-template sessions observed in the reconciler
trace after each kill. Each replacement reached `serve start` in its rig trace.

| Killed ID | Dispatcher / rig | Kill (EDT) | Attributed set / age | First replacement (`start-pending`) | `serve start` (UTC) | Post-reset conversion evidence |
|---|---|---:|---|---|---:|---|
| `gc-519760` | `mem/core.control-dispatcher` | 01:17:25 | same city-wide 66 / 104m12s | `gc-521242` at 05:32:25Z | 05:32:33Z | none through 15:03Z |
| `gc-519753` | `gascity/core.control-dispatcher` | 01:17:47 | same city-wide 66 / 104m34s | `gc-521236` at 05:31:27Z | 05:31:36Z | none through 15:03Z |
| `gc-519748` | `gascity-packs/core.control-dispatcher` | 01:18:05 | same city-wide 66 / 104m52s | `gc-521231` at 05:30:41Z | 05:30:52Z | first original-set conversion was `gpk-rt7km` at 10:41Z, over five hours later; later replacements were involved |
| `gc-519742` | `gascity-dashboard/core.control-dispatcher` | 01:18:19 | same city-wide 66 / 105m06s | `gc-521222` at 05:29:33Z | 05:29:47Z | none through 15:03Z |
| `gc-519736` | `enterprisebench/core.control-dispatcher` | 01:18:34 | same city-wide 66 / 105m21s | `gc-521216` at 05:28:10Z | 05:28:26Z | none through 15:03Z |
| `gc-519729` | city `core.control-dispatcher` | 01:18:50 | same city-wide 66 / 105m37s | `gc-521207` at 05:27:18Z | 05:27:33Z | none through 15:03Z |
| `gc-519695` | `codeprobe/core.control-dispatcher` | 01:19:05 | same city-wide 66 / 105m52s | `gc-521168` at 05:20:33Z | 05:20:39Z | none through 15:03Z |

The large replacement delay for six dispatchers is visible in the reconciler:
the old sessions transition to `runtime-missing`, then the pool floor creates a
new same-template session. This confirms that replacements happened; it does
not establish that each killed process was defective.

## Reset did not prove recovery

The replacement entries were all initialized at the same later time,
`2026-07-20T05:56:03Z`, with the same original 66-ID city-wide set. Thus a reset
only gave every new ID a fresh 90-minute grace clock; it did not itself remove
or convert the set.

The set eventually changed from 66 to 63 by 14:33:06Z. Relative to the killed
snapshot, five `gpk-*` IDs disappeared and two unrelated IDs appeared. Rig
traces prove delayed processing of original-set `gpk-rt7km` at 10:41Z and
`gpk-p9b2f` at 11:47Z. They do not support attributing those conversions to the
seven resets: the conversions happened hours later and after subsequent
session/city lifecycle events. Six affected rig traces contain no
`serve processed` record after the reset through 15:03Z, although all trace
tails remained fresh with event/idle activity.

Two other dispatcher IDs (`gc-519693`, `gc-519694`) held the same 66-ID set and
same clock but were not in the seven conversion-stall kills; the next watchdog
run killed them on the independent five-hour idle signal at 01:56–01:57 EDT.
This further shows that the 66-ID set was global shared input rather than proof
about one dispatcher's health.

## Required correction before conversion-stall may mutate sessions

The conversion signal must compare only beads actually owned by the candidate
dispatcher. At minimum, `control_pending_set` must accept the dispatcher's
template/name and exactly normalize and match `gc.routed_to` / `gc.run_target`
to that city or rig target. Substring matching `control-dispatcher` is not an
ownership test. A safe implementation also needs hermetic cases proving:

1. a stalled bead for rig A cannot advance or trip rig B's clock;
2. unrelated arrivals/removals cannot reset another dispatcher's clock;
3. bare city, dotted city, and `<rig>/` target forms normalize correctly;
4. unreadable rig data remains fail-closed (no kill);
5. a replacement ID does not erase evidence unless its own scoped set changes.

No runtime action or code/config/threshold change was made during this
investigation. In particular, no dispatcher was killed, restarted, nudged, or
force-spawned, and SciX remained untouched.

## Reproduction / verification commands

```bash
# Source inspection
nl -ba bin/dispatcher-watchdog | sed -n '55,119p;142,192p'

# Exact kill sequence
grep -nE '^2026-07-20T01:(17|18|19):' .gc/dispatcher-watchdog.log

# State equivalence (all seven print 66 and the same hash/since)
python3 - <<'PY'
import datetime, hashlib, json
ids = ['gc-519760','gc-519753','gc-519748','gc-519742',
       'gc-519736','gc-519729','gc-519695']
d = json.load(open('.gc/dispatcher-watchdog-conversion.json'))
for i in ids:
    v = d[i]
    print(i, len(v['set'].split(',')),
          datetime.datetime.fromtimestamp(v['since'], datetime.timezone.utc).isoformat(),
          hashlib.sha256(v['set'].encode()).hexdigest()[:16])
assert len({d[i]['set'] for i in ids}) == 1
assert len({d[i]['since'] for i in ids}) == 1
PY

# Replacement identity: parse session_baseline records in
# .gc/runtime/session-reconciler-trace/segments/2026/07/20/*.jsonl and select
# the first different session ID for each same-template dispatcher after kill.

# Trace proof
awk '$1 >= "2026-07-20T05:17:00" && /serve processed/' \
  .gc/runtime/{mem--,gascity--,gascity-packs--,gascity-dashboard--,enterprisebench--,codeprobe--,}core.control-dispatcher-trace.log
```
