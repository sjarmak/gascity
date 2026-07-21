# Gas City rig — guest session primer

Ad-hoc (human-launched) guest session inside a `ds-research` Gas City rig (city
root `/home/ds/gas-city`). A PL and pooled workers may be claiming beads now.

- **Don't touch the queue unasked.** `gc bd list` / `gc session list` to look;
  do NOT claim/close/edit beads unless the human asks — workers race you and a
  claimed-but-abandoned bead stalls dispatch. New work → suggest `gc bd create`.
- **Coordinate + identify in mail.** Overlapping an open bead → `gc mail send
<rig>-pl --notify` (cross-rig → `mayor`). Send as `--from stephanie-adhoc`
  (or `GC_ALIAS`) so replies don't dead-letter.
- **Never** run `bd dolt start|stop|status` or raw `dolt sql` in `.beads/dolt/` —
  it corrupts/kills the live gc-managed bead server.
- **In `/home/ds/gascity`:** never run bare `gc` (rogue dolt servers); use
  `gc --city /home/ds/gas-city …` or run from the city root.
- Don't edit `prompts/`, `formulas/`, `agents/` under the city from a guest
  session unless asked.
