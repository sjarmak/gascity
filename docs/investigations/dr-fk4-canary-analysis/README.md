# dr-fk4 canary analysis — data extraction

Report: `docs/dr-fk4-hybrid-claim-canary-verification-2026-07-19.md`

The three scripts consume CSVs extracted from the city Dolt server (read the
port from `.beads/dolt/.dolt/sql-server.info`; it was 29620). Regenerate them
with, per rig `$db` in {EnterpriseBench, mem, gascity}:

```sql
-- claims_<db>.csv (analyze.py / analyze2.py)
SELECT e.issue_id, e.actor, e.created_at AS claim_time, i.priority, i.issue_type,
       i.created_at AS bead_created,
       TIMESTAMPDIFF(SECOND, i.created_at, e.created_at) AS age_at_claim_s
FROM `$db`.events e JOIN `$db`.issues i ON i.id = e.issue_id
WHERE e.event_type='claimed' AND e.created_at >= '2026-07-13 13:33:00'
ORDER BY e.created_at;

-- issues_<db>.csv (analyze.py / analyze2.py)
SELECT i.id, i.priority, i.issue_type, i.status, i.created_at, i.closed_at, i.assignee,
       (SELECT MIN(e.created_at) FROM `$db`.events e
        WHERE e.issue_id=i.id AND e.event_type='claimed') AS first_claim
FROM `$db`.issues i WHERE i.issue_type NOT IN ('epic');

-- claims_<db>_raw.csv (analyze3.py; old_value = full pre-claim issue JSON)
SELECT e.issue_id, e.actor, e.created_at AS claim_time, e.old_value
FROM `$db`.events e
WHERE e.event_type='claimed' AND e.created_at >= '2026-07-13 13:33:00'
ORDER BY e.created_at;
```

EB pre-flip baseline (`claims_EB_preflip.csv`): same as the first query with
the window `>= '2026-07-07 13:33:00' AND < '2026-07-13 13:33:00'`.

Timezone trap the scripts correct for: `issues.*` datetimes are UTC,
`events.*` datetimes are EDT (UTC-4); the flip 2026-07-13T17:33Z is
`2026-07-13 13:33:00` in event time.
