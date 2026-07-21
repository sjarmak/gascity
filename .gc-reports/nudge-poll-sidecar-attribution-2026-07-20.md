# Nudge-poll sidecar lifecycle and SciX attribution — 2026-07-20

Bead: `dr-3z4k`

## Finding

Alert `gc-524120` correctly counted processes whose command line identifies
the SciX MCP server, but its label did not identify which sessions owned those
processes. `bin/resource-observability-sample:83-98` classifies solely from
command-line substrings and `:127-148` aggregates process count/RSS by MCP
server type. It does not inspect cgroups, process ancestry, tmux sessions, or
Gas City session IDs. Therefore “MCP 'scix' fan-out” means “copies of the SciX
MCP executable”, not “processes owned by the scix-experiments rig”.

The alert also did not identify the more urgent resource consumer because its
generic hot-loop signal requires over 150% CPU (`:134`, `:168-171`). The known
`gc nudge poll` failure stabilizes near 50% CPU and is owned by the dedicated
`nudge-poll-reaper` instead.

## Exact PID 3957760 ownership and lifecycle

Before containment:

- command: `/home/ds/go/bin/gc nudge poll --city /home/ds/gas-city --session scix-experiments-pl gc-507722`
- start: 2026-07-20 10:09:54 EDT
- observation at 11:07:49 EDT: age 3,475 seconds, 50.1% lifetime CPU,
  approximately 544 MiB RSS, 20 threads
- target session ID: `gc-507722`
- target alias/template: `scix-experiments-pl`
- target work directory: `/home/ds/projects/scix_experiments`
- target session remained `active`; its tmux session and Amp process were live
- rig `scix-experiments` remained suspended
- PPID: user systemd PID 1403
- cgroup: `/user.slice/user-1000.slice/user@1000.service/app.slice/gascity-supervisor.service`

PPID 1403 alone is not proof of a stale target. The source intentionally starts
the poller, places it in a separate process group, and calls
`cmd.Process.Release()` (`/home/ds/gascity-main/internal/session/submit.go:597-629`),
so reparenting to the user manager is expected. The proof of malfunction is the
combined process identity plus sustained CPU/RSS signature: a healthy event
poller is near 0% CPU, while this was the only `gc nudge poll` process and met
the installed incident-specific reaper predicate.

The exact lease stem was `scix-experiments-pl-170508d01363eca8`. Its log
recorded pprof bind failure plus repeated tmux/process-snapshot degradation at
10:12–10:20 EDT. After termination, the `.pid` lease disappeared while the
stable `.pid.lock` remained, matching the source lease contract.

## Containment and verification

Before acting, the existing mitigation was run in dry-run mode:

```text
WOULD-REAP pid=3957760 cpu=50.1% age=3475s session=scix-experiments-pl gc-507722
would-reap 1 proc(s), ~50% CPU
```

This matched exactly one process. PID 3957760 was sent `SIGTERM` directly; it
exited within two seconds without escalation. Post-checks proved:

- `/proc/3957760` absent;
- no remaining `gc nudge poll` process;
- `bin/nudge-poll-reaper --dry-run` reported no leaked pollers;
- `gc-507722` remained active;
- `scix-experiments` remained suspended;
- no MCP process was signalled;
- no service, dispatcher, worker, or supervisor was killed/restarted;
- SciX was not resumed.

The containment reclaimed approximately 544 MiB RSS and 50% of one CPU.

## MCP ownership map

Process ancestry and tmux pane ownership showed the copies observed during the
investigation belonged to independent Claude sessions because
`/home/ds/.claude/.mcp.json:10-20` globally enables `python -m
scix.mcp_server`:

| MCP PID | Owning tmux/session | Actual owner |
|---:|---|---|
| 454199 | `scix-experiments--oversight-rig__project-lead` / `gc-524066` | live SciX audit project lead |
| 514222 | `claude-4-gc-524076` | AOA Claude worker; exited normally during investigation |
| 516535 | `claude-5-gc-524077` | AOA Claude worker |
| 641163 | `claude-2-gc-518004` | AOA Claude worker, roughly 21.6 hours old |
| 1217444 | `claude-3-gc-524075` | transient AOA replacement/resume; exited during investigation |

The population changed as AOA workers drained/resumed, confirming that command
name is not project ownership. No MCP was blanket-killed or otherwise changed.

## Residual defects

1. Resource-observability should present MCP server type separately from
   owning session/template/rig; its current aggregate is operationally
   ambiguous.
2. Released pollers are intentionally reparented, so orphan detection must not
   use PPID alone. Exact command/lease identity plus sustained busy-loop
   evidence is the safe containment predicate.
3. The dedicated reaper did not act before the manual investigation. At
   observation time it did classify the process exactly; the likely timing
   explanation is that lifetime CPU only crossed its inclusive 50% threshold
   near the investigation. No threshold was changed.

## Verification commands

```bash
ps -p 3957760 -o pid,ppid,lstart,etimes,pcpu,rss,stat,args
cat /proc/3957760/cgroup
pstree -aps 3957760
gc session list --json | jq '.sessions[] | select(.id=="gc-507722")'
gc rig list | grep -i -C1 scix
bin/nudge-poll-reaper --dry-run
ps -eo pid,ppid,etimes,pcpu,rss,args --no-headers | grep '[g]c nudge poll'
```
