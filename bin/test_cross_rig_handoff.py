import json
import os
import subprocess
from pathlib import Path

import pytest


SCRIPT = Path(__file__).with_name("cross-rig-handoff")


FAKE = r'''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
p=Path(os.environ["FAKE_STATE"]); s=json.loads(p.read_text())
name=Path(sys.argv[0]).name; a=sys.argv[1:]
def save(): p.write_text(json.dumps(s))
if name == "gc-sling":
    route, child=a[0], a[1]
    if s.get("fail_route_rig") == child.split("-")[0]: sys.exit(9)
    for rows in s["rigs"].values():
        if child in rows: rows[child].setdefault("metadata",{})["gc.routed_to"]=route
    save(); sys.exit(0)
if name == "bd":
    action, ident=a[0], a[1]
    if action == "show": print(json.dumps([s["city"][ident]])); sys.exit(0)
    if action == "reopen": s["city"][ident]["status"]="open"; save(); sys.exit(0)
if a[:2] == ["session", "nudge"]: s["nudges"]=s.get("nudges",0)+1; save(); sys.exit(0)
assert a[:2] == ["bd", "--rig"], a
rig=a[2]; action=a[3]; rest=a[4:]; rows=s["rigs"].setdefault(rig,{})
if action == "list":
    filters=[rest[i+1].split("=",1) for i,x in enumerate(rest) if x == "--metadata-field"]
    print(json.dumps([v for v in rows.values() if all(str(v.get("metadata",{}).get(k,""))==val for k,val in filters)])); sys.exit(0)
if action == "show": print(json.dumps([rows[rest[0]]])); sys.exit(0)
if action == "create":
    if s.get("fail_create_rig") == rig: sys.exit(8)
    def val(flag): return rest[rest.index(flag)+1]
    ident=f"{rig}-{len(rows)+1}"; rows[ident]={"id":ident,"title":val("--title"),"status":"open","assignee":"","metadata":json.loads(val("--metadata"))}
    save(); print(json.dumps(rows[ident])); sys.exit(0)
if action == "reopen": rows[rest[0]]["status"]="open"; save(); sys.exit(0)
raise SystemExit(3)
'''


def target(key, rig):
    return {"target_key": key, "rig": rig, "store_ref": f"rig:{rig}", "route_to": f"{rig}-pl",
            "work": {"title": f"Work {key}", "description": "Complete it", "acceptance_criteria": "Tests pass",
                     "type": "task", "priority": 2, "labels": ["test"]}}


@pytest.fixture
def env(tmp_path):
    bindir=tmp_path/"bin"; bindir.mkdir(); fake=bindir/"fake"; fake.write_text(FAKE); fake.chmod(0o755)
    for name in ("gc", "bd", "gc-sling"): (bindir/name).symlink_to(fake)
    state=tmp_path/"state.json"; state.write_text(json.dumps({"city":{
        "dr-zkmc":{"id":"dr-zkmc","status":"closed"},
        "dr-xu0u":{"id":"dr-xu0u","status":"open","title":"tracker only","assignee":""}
    },"rigs":{}}))
    e={**os.environ, "PATH":f"{bindir}:{os.environ['PATH']}", "FAKE_STATE":str(state),
       "CROSS_RIG_HANDOFF_DIR":str(tmp_path/"records"), "CROSS_RIG_HANDOFF_AUDIT_LOG":str(tmp_path/"audit.jsonl"),
       "CROSS_RIG_HANDOFF_LOCK":str(tmp_path/"lock"),
       "CROSS_RIG_HANDOFF_INVALID_NOTIFY_STATE":str(tmp_path/"invalid.notified")}
    return e, state, tmp_path


def run(env, *args, spec=None):
    cmd=[str(SCRIPT), *args]
    if spec is not None:
        path=Path(env["CROSS_RIG_HANDOFF_DIR"]).parent/"spec.json"; path.write_text(json.dumps(spec)); cmd += ["--spec", str(path)]
    return subprocess.run(cmd, env=env, text=True, capture_output=True)


def test_two_rigs_route_and_tracker_only_is_not_gate(env):
    e,state,_=env
    assert run(e,"gate","--source","city:dr-zkmc").returncode != 0  # closed tracker alone is insufficient
    spec={"handoff_id":"h-success","source_ref":"city:dr-zkmc","targets":[target("a","alpha"),target("b","beta")]}
    result=run(e,"materialize",spec=spec)
    assert result.returncode == 0, result.stderr
    record=json.loads(result.stdout)
    assert [x["state"] for x in record["targets"]] == ["accepted","accepted"]
    assert [x["child_ref"] for x in record["targets"]] == ["rig:alpha:alpha-1","rig:beta:beta-1"]
    assert run(e,"gate","--source","city:dr-zkmc").returncode == 0
    live=json.loads(state.read_text())
    assert live["rigs"]["alpha"]["alpha-1"]["metadata"]["gc.routed_to"] == "alpha-pl"
    assert live["rigs"]["beta"]["beta-1"]["metadata"]["gc.routed_to"] == "beta-pl"

    # Real gc-sling may persist a canonical path instead of the caller alias.
    live["rigs"]["alpha"]["alpha-1"]["metadata"]["gc.routed_to"] = "/canonical/alpha-worker"
    state.write_text(json.dumps(live))
    assert run(e,"materialize","--id","h-success").returncode == 0


def test_partial_persists_reopens_and_retry_only_missing(env):
    e,state,_=env; live=json.loads(state.read_text()); live["fail_create_rig"]="beta"; state.write_text(json.dumps(live))
    spec={"handoff_id":"h-partial","source_ref":"city:dr-zkmc","targets":[target("a","alpha"),target("b","beta")]}
    first=run(e,"materialize",spec=spec)
    assert first.returncode == 1
    record=json.loads(Path(e["CROSS_RIG_HANDOFF_DIR"],"h-partial.json").read_text())
    assert [x["state"] for x in record["targets"]] == ["accepted","failed"]
    assert json.loads(state.read_text())["city"]["dr-zkmc"]["status"] == "open"
    live=json.loads(state.read_text()); live["city"]["dr-zkmc"]["status"]="closed"; state.write_text(json.dumps(live))
    assert run(e,"scan","--apply").returncode == 1
    live=json.loads(state.read_text()); assert live["city"]["dr-zkmc"]["status"] == "open"
    assert len(live["rigs"]["alpha"]) == 1
    del live["fail_create_rig"]; state.write_text(json.dumps(live))
    assert run(e,"materialize","--id","h-partial").returncode == 0
    live=json.loads(state.read_text()); assert len(live["rigs"]["alpha"]) == 1 and len(live["rigs"]["beta"]) == 1


def test_open_unassigned_blocked_reason_and_duplicate_fail_closed(env):
    e,state,_=env
    spec={"handoff_id":"h-state","source_ref":"city:dr-zkmc","targets":[target("a","alpha")]}
    assert run(e,"materialize",spec=spec).returncode == 0
    live=json.loads(state.read_text()); child=live["rigs"]["alpha"]["alpha-1"]
    child["metadata"].pop("gc.routed_to", None); state.write_text(json.dumps(live))
    assert run(e,"scan").returncode == 1  # dry-run does not route
    assert run(e,"gate","--source","city:dr-zkmc").returncode == 1  # gate re-reads target state
    assert "gc.routed_to" not in json.loads(state.read_text())["rigs"]["alpha"]["alpha-1"]["metadata"]
    live=json.loads(state.read_text()); child=live["rigs"]["alpha"]["alpha-1"]
    child["status"]="blocked"; child["metadata"]["cross_rig_handoff_blocked_reason"]="waiting on API"; state.write_text(json.dumps(live))
    assert run(e,"materialize","--id","h-state").returncode == 0
    live=json.loads(state.read_text()); live["rigs"]["alpha"]["alpha-duplicate"]={**child,"id":"alpha-duplicate"}; state.write_text(json.dumps(live))
    assert run(e,"materialize","--id","h-state").returncode == 1

    # A persisted child whose query provenance is altered must fail closed,
    # not materialize a replacement that leaves the original orphaned.
    del live["rigs"]["alpha"]["alpha-duplicate"]
    live["rigs"]["alpha"]["alpha-1"]["metadata"]["cross_rig_handoff_target_key"] = "tampered"
    state.write_text(json.dumps(live))
    assert run(e,"materialize","--id","h-state").returncode == 1
    assert len(json.loads(state.read_text())["rigs"]["alpha"]) == 1


def test_immutable_reuse_and_six_lifecycle_events(env):
    e,state,tmp=env
    specs=[
        {"handoff_id":"accepted","source_ref":"city:dr-zkmc","targets":[target("a","alpha")]},
        {"handoff_id":"completed","source_ref":"city:dr-zkmc","targets":[target("b","beta")]},
        {"handoff_id":"blocked","source_ref":"city:dr-zkmc","targets":[target("c","gamma")]},
        {"handoff_id":"failed","source_ref":"city:dr-zkmc","targets":[target("d","delta")]},
    ]
    for spec in specs[:3]: assert run(e,"materialize",spec=spec).returncode == 0
    live=json.loads(state.read_text())
    live["rigs"]["beta"]["beta-1"]["status"]="closed"
    g=live["rigs"]["gamma"]["gamma-1"]; g["status"]="blocked"; g["metadata"]["help_request"]="need review"
    live["fail_create_rig"]="delta"; state.write_text(json.dumps(live))
    assert run(e,"materialize","--id","completed").returncode == 0
    assert run(e,"materialize","--id","blocked").returncode == 0
    assert run(e,"materialize",spec=specs[3]).returncode == 1
    changed=json.loads(json.dumps(specs[0])); changed["targets"][0]["work"]["title"]="changed"
    assert run(e,"materialize",spec=changed).returncode == 2
    events={json.loads(line)["event"] for line in (tmp/"audit.jsonl").read_text().splitlines()}
    assert {"declared","materializing","accepted","blocked","failed","completed"} <= events


def test_stale_record_does_not_block_valid_repair_or_mayor_nudge(env):
    e,state,tmp=env
    spec={"handoff_id":"valid-after-stale","source_ref":"city:dr-zkmc","targets":[target("a","alpha")]}
    live=json.loads(state.read_text()); live["fail_create_rig"]="alpha"; state.write_text(json.dumps(live))
    assert run(e,"materialize",spec=spec).returncode == 1
    records=Path(e["CROSS_RIG_HANDOFF_DIR"])
    (records/"00-stale.json").write_text('{"version":0,"handoff_id":"stale"}')

    result=run(e,"scan","--apply","--nudge-mayor")
    assert result.returncode == 1
    live=json.loads(state.read_text())
    assert live["city"]["dr-zkmc"]["status"] == "open"
    assert live["nudges"] == 1
    summary=json.loads(result.stdout)
    assert summary["invalid_records"] == ["00-stale.json"]
    assert "valid-after-stale" in summary["unresolved"]
    assert any(json.loads(line)["event"] == "record_invalid" for line in (tmp/"audit.jsonl").read_text().splitlines())


def test_typed_malformed_record_isolated_and_unrelated_gate_still_works(env):
    e,state,_=env
    good={"handoff_id":"good","source_ref":"city:dr-zkmc","targets":[target("a","alpha")]}
    assert run(e,"materialize",spec=good).returncode == 0
    records=Path(e["CROSS_RIG_HANDOFF_DIR"])
    malformed=json.loads((records/"good.json").read_text())
    malformed["handoff_id"]="bad-types"
    malformed["source_ref"]="city:dr-xu0u"
    malformed["targets"][0]["route_to"]=["not", "a", "string"]
    (records/"bad-types.json").write_text(json.dumps(malformed))

    result=run(e,"scan","--apply")
    assert result.returncode == 1
    assert json.loads(result.stdout)["invalid_records"] == ["bad-types.json"]
    assert run(e,"gate","--source","city:dr-zkmc").returncode == 0
    assert run(e,"gate","--source","city:dr-xu0u").returncode == 2


def test_persisted_immutable_intent_tamper_fails_closed(env):
    e,_,_=env
    spec={"handoff_id":"immutable","source_ref":"city:dr-zkmc","targets":[target("a","alpha")]}
    assert run(e,"materialize",spec=spec).returncode == 0
    path=Path(e["CROSS_RIG_HANDOFF_DIR"],"immutable.json")
    record=json.loads(path.read_text())
    record["targets"][0]["route_to"]="different-worker"
    path.write_text(json.dumps(record))

    assert run(e,"materialize","--id","immutable").returncode == 2
    assert run(e,"materialize",spec=spec).returncode == 2
    assert run(e,"gate","--source","city:dr-zkmc").returncode == 2
