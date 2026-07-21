"""Regression tests for the central Gas City GitHub Issues mirror.

Run with: python3 -m pytest -q bin/test_github_mirror_central.py
"""

from __future__ import annotations

import importlib.machinery
import importlib.util
import json
import sys
from pathlib import Path

import pytest


BIN = Path(__file__).resolve().parent
sys.path.insert(0, str(BIN))


def load_script(module_name: str, filename: str):
    loader = importlib.machinery.SourceFileLoader(module_name, str(BIN / filename))
    spec = importlib.util.spec_from_loader(module_name, loader)
    module = importlib.util.module_from_spec(spec)
    sys.modules[module_name] = module
    loader.exec_module(module)
    return module


def test_parse_named_rig_specs_preserves_city_identity():
    from github_mirror_common import parse_rig_specs

    specs = parse_rig_specs(
        "ds-research=/home/ds/gas-city "
        "scix-experiments=/home/ds/projects/scix_experiments"
    )

    assert [(spec.name, spec.path) for spec in specs] == [
        ("ds-research", "/home/ds/gas-city"),
        ("scix-experiments", "/home/ds/projects/scix_experiments"),
    ]


def test_parse_legacy_path_specs_remains_backward_compatible():
    from github_mirror_common import parse_rig_specs

    specs = parse_rig_specs("/home/ds/projects/mem /home/ds/projects/codeprobe")

    assert [(spec.name, spec.path) for spec in specs] == [
        ("mem", "/home/ds/projects/mem"),
        ("codeprobe", "/home/ds/projects/codeprobe"),
    ]


def test_rest_issue_pages_flatten_and_exclude_pull_requests():
    from github_mirror_common import flatten_issue_pages

    issues = flatten_issue_pages(
        [
            [
                {"number": 1, "html_url": "https://example/1", "labels": []},
                {
                    "number": 2,
                    "html_url": "https://example/2",
                    "labels": [],
                    "pull_request": {"url": "https://api.example/pr/2"},
                },
            ],
            [{"number": 3, "html_url": "https://example/3", "labels": []}],
        ]
    )

    assert [issue["number"] for issue in issues] == [1, 3]


def test_central_issue_rendering_has_exact_structural_labels_and_provenance():
    from github_mirror_common import RigSpec, render_central_issue

    desired = render_central_issue(
        RigSpec("mem", "/home/ds/projects/mem"),
        {
            "id": "mem-abc",
            "title": "Repair retrieval gate",
            "description": "Full private detail.",
            "issue_type": "bug",
            "priority": 1,
            "status": "blocked",
            "labels": ["needs-human", "rig:wrong"],
        },
    )

    assert desired["labels"] == [
        "rig:mem",
        "type::bug",
        "priority::high",
        "status::blocked",
    ]
    assert desired["state"] == "open"
    assert "Full private detail." in desired["body"]
    assert "Bead: `mem-abc`" in desired["body"]
    assert '<!-- gas-city-bead {"id":"mem-abc","rig":"mem"} -->' in desired["body"]


def test_central_marker_round_trips_without_title_matching():
    from github_mirror_common import RigSpec, marker_key, render_central_issue

    desired = render_central_issue(
        RigSpec("aoa", "/home/ds/projects/aoa"),
        {
            "id": "aoa-42",
            "title": "A duplicated title is fine",
            "description": "",
            "issue_type": "task",
            "priority": 2,
            "status": "open",
        },
    )

    assert marker_key(desired["body"]) == ("aoa", "aoa-42")
    assert marker_key("A duplicated title is fine") is None


def test_central_marker_must_be_unique_canonical_and_final():
    from github_mirror_common import marker_key

    marker = '<!-- gas-city-bead {"id":"aoa-42","rig":"aoa"} -->'

    with pytest.raises(ValueError, match="final line"):
        marker_key(marker + "\ntrailing text")
    with pytest.raises(ValueError, match="exactly one"):
        marker_key(marker + "\n" + marker)
    with pytest.raises(ValueError, match="canonical"):
        marker_key('<!-- gas-city-bead {"rig":"aoa", "id":"aoa-42"} -->')


def test_bead_description_cannot_forge_central_identity():
    from github_mirror_common import RigSpec, build_central_plan

    marker = '<!-- gas-city-bead {"id":"other","rig":"mem"} -->'
    bead = {
        "id": "mem-real",
        "title": "Untrusted description",
        "description": marker,
        "issue_type": "task",
        "priority": 2,
        "status": "open",
    }

    plan = build_central_plan(
        [(RigSpec("mem", "/home/ds/projects/mem"), bead)], [], set()
    )

    assert plan[0]["kind"] == "invalid"
    assert "marker" in plan[0]["error"]


def test_central_plan_updates_by_marker_and_preserves_foreign_external_ref():
    from github_mirror_common import RigSpec, build_central_plan, render_central_issue

    spec = RigSpec("gascity", "/home/ds/gascity")
    bead = {
        "id": "gc-qxem",
        "title": "Keep upstream PR backlink",
        "description": "Description changed locally.",
        "issue_type": "task",
        "priority": 1,
        "status": "in_progress",
        "external_ref": "https://github.com/gastownhall/gascity/pull/4424",
    }
    old = render_central_issue(spec, {**bead, "description": "Old description."})
    remote = {
        "number": 9,
        "url": "https://github.com/sjarmak/gas-city-beads/issues/9",
        "title": old["title"],
        "body": old["body"],
        "state": old["state"],
        "labels": old["labels"],
    }

    plan = build_central_plan([(spec, bead)], [remote], set())

    assert [action["kind"] for action in plan] == ["update"]
    assert plan[0]["bead"]["external_ref"].endswith("/pull/4424")


def test_central_plan_transfers_only_explicit_legacy_mirror_refs():
    from github_mirror_common import RigSpec, build_central_plan

    spec = RigSpec("mem", "/home/ds/projects/mem")
    legacy = {
        "id": "mem-old",
        "title": "Existing pilot issue",
        "description": "",
        "issue_type": "task",
        "priority": 2,
        "status": "open",
        "external_ref": "https://github.com/sjarmak/mem-beads/issues/10",
    }
    upstream = {
        **legacy,
        "id": "mem-upstream",
        "external_ref": "https://github.com/other/upstream/issues/11",
    }

    plan = build_central_plan(
        [(spec, legacy), (spec, upstream)],
        [],
        {"sjarmak/mem-beads", "sjarmak/codeprobe-beads"},
    )

    assert [(action["bead"]["id"], action["kind"]) for action in plan] == [
        ("mem-old", "transfer"),
        ("mem-upstream", "create"),
    ]


def test_central_plan_fails_closed_on_github_field_limits():
    from github_mirror_common import RigSpec, build_central_plan

    spec = RigSpec("mem", "/home/ds/projects/mem")
    bead = {
        "id": "mem-oversize",
        "title": "x" * 257,
        "description": "y" * 70_000,
        "issue_type": "task",
        "priority": 2,
        "status": "open",
    }

    plan = build_central_plan([(spec, bead)], [], set())

    assert plan[0]["kind"] == "invalid"
    assert "title exceeds" in plan[0]["error"]
    assert "body exceeds" in plan[0]["error"]


def test_central_plan_fails_closed_on_invalid_structural_label():
    from github_mirror_common import RigSpec, build_central_plan

    bead = {
        "id": "mem-bad-label",
        "title": "Bad label",
        "description": "",
        "issue_type": "x" * 60,
        "priority": 2,
        "status": "open",
    }

    plan = build_central_plan(
        [(RigSpec("mem", "/home/ds/projects/mem"), bead)], [], set()
    )

    assert plan[0]["kind"] == "invalid"
    assert "label exceeds 50" in plan[0]["error"]


def test_central_plan_refuses_duplicate_remote_and_local_identities():
    from github_mirror_common import RigSpec, build_central_plan, render_central_issue

    spec = RigSpec("mem", "/home/ds/projects/mem")
    bead = {
        "id": "mem-dup",
        "title": "Duplicate",
        "description": "",
        "issue_type": "task",
        "priority": 2,
        "status": "open",
    }
    desired = render_central_issue(spec, bead)
    remote = {
        "number": 1,
        "url": "https://github.com/sjarmak/gas-city-beads/issues/1",
        **desired,
    }

    with pytest.raises(ValueError, match="duplicate central mirror marker"):
        build_central_plan([(spec, bead)], [remote, {**remote, "number": 2}], set())
    with pytest.raises(ValueError, match="duplicate bead identity"):
        build_central_plan([(spec, bead), (spec, bead)], [], set())


def test_central_plan_refuses_duplicate_legacy_transfer_source():
    from github_mirror_common import (
        LEGACY_TRANSFER_REPOSITORIES,
        RigSpec,
        build_central_plan,
    )

    spec = RigSpec("mem", "/home/ds/projects/mem")
    base = {
        "title": "Duplicate source",
        "description": "",
        "issue_type": "task",
        "priority": 2,
        "status": "open",
        "external_ref": "https://github.com/sjarmak/mem-beads/issues/7",
    }

    with pytest.raises(ValueError, match="duplicate legacy transfer source"):
        build_central_plan(
            [(spec, {**base, "id": "mem-a"}), (spec, {**base, "id": "mem-b"})],
            [],
            LEGACY_TRANSFER_REPOSITORIES,
        )


def test_unlinked_pilot_bead_is_deferred_while_transfers_remain():
    from github_mirror_common import (
        LEGACY_TRANSFER_REPOSITORIES,
        RigSpec,
        build_central_plan,
    )

    spec = RigSpec("mem", "/home/ds/projects/mem")
    base = {
        "title": "Pilot transition",
        "description": "",
        "issue_type": "task",
        "priority": 2,
        "status": "open",
    }
    plan = build_central_plan(
        [
            (spec, {**base, "id": "mem-new"}),
            (
                spec,
                {
                    **base,
                    "id": "mem-existing",
                    "external_ref": "https://github.com/sjarmak/mem-beads/issues/7",
                },
            ),
        ],
        [],
        LEGACY_TRANSFER_REPOSITORIES,
    )

    assert [(action["bead"]["id"], action["kind"]) for action in plan] == [
        ("mem-new", "deferred"),
        ("mem-existing", "transfer"),
    ]


def test_exact_private_target_preflight_fails_closed(monkeypatch):
    central = load_script("github_central_preflight_test", "github-central-mirror")
    queried = []

    monkeypatch.setattr(
        central,
        "api_json",
        lambda _method, endpoint, _payload=None: queried.append(endpoint)
        or {
            "full_name": central.TARGET_REPOSITORY,
            "private": False,
            "has_issues": True,
        },
    )

    with pytest.raises(RuntimeError, match="exactly"):
        central.preflight_target_repository("sjarmak/not-the-private-view")
    assert queried == []
    with pytest.raises(RuntimeError, match="private"):
        central.preflight_target_repository(central.TARGET_REPOSITORY)


def test_execute_preflight_happens_before_any_mutation(monkeypatch):
    central = load_script("github_central_mutation_preflight_test", "github-central-mirror")
    mutated = []

    monkeypatch.setattr(
        central,
        "preflight_target_repository",
        lambda _repository: (_ for _ in ()).throw(RuntimeError("public target")),
    )
    monkeypatch.setattr(
        central, "create_labels", lambda _repo, _labels: mutated.append("labels")
    )

    with pytest.raises(RuntimeError, match="public target"):
        central.apply_plan(central.TARGET_REPOSITORY, [], {"rig:mem"})
    assert mutated == []


def test_process_lock_rejects_overlapping_cycle(tmp_path):
    central = load_script("github_central_lock_test", "github-central-mirror")
    lock = tmp_path / "mirror.lock"

    with central.process_lock(lock):
        with pytest.raises(RuntimeError, match="already running"):
            with central.process_lock(lock):
                pass


def test_legacy_transfer_sources_are_immutable():
    central = load_script("github_central_legacy_source_test", "github-central-mirror")

    assert central.parse_legacy_repositories(
        "sjarmak/mem-beads,sjarmak/codeprobe-beads"
    ) == central.LEGACY_TRANSFER_REPOSITORIES
    with pytest.raises(ValueError, match="must be exactly"):
        central.parse_legacy_repositories("other/private-beads")
    with pytest.raises(ValueError, match="must be exactly"):
        central.parse_legacy_repositories("sjarmak/mem-beads")


def test_label_writes_have_a_fail_closed_cap(monkeypatch):
    central = load_script("github_central_label_cap_test", "github-central-mirror")
    mutated = []

    monkeypatch.setattr(central, "preflight_target_repository", lambda _repository: None)
    monkeypatch.setenv("MIRROR_MAX_LABEL_CREATE", "1")
    monkeypatch.setattr(
        central, "create_labels", lambda _repo, _labels: mutated.append("labels")
    )

    with pytest.raises(RuntimeError, match="label creations"):
        central.apply_plan(central.TARGET_REPOSITORY, [], {"rig:mem", "rig:aoa"})
    assert mutated == []


def test_transfer_recovers_after_post_transfer_patch_interruption(monkeypatch, tmp_path):
    central = load_script("github_central_transfer_recovery_test", "github-central-mirror")
    from github_mirror_common import RigSpec

    old_url = "https://github.com/sjarmak/mem-beads/issues/7"
    new_url = "https://github.com/sjarmak/gas-city-beads/issues/12"
    action = {
        "kind": "transfer",
        "spec": RigSpec("mem", "/home/ds/projects/mem"),
        "bead": {"id": "mem-7", "external_ref": old_url},
        "desired": {"title": "Transferred", "body": "body", "state": "open", "labels": []},
        "remote": None,
    }
    state_path = tmp_path / "migration.json"
    state = {"transfers": {}}
    transferred = False
    transfer_calls = 0
    patch_calls = 0

    def fake_resolve(_node_id):
        return new_url if transferred else old_url

    def fake_run(cmd, **_kwargs):
        nonlocal transferred, transfer_calls
        assert cmd[:3] == ["gh", "issue", "transfer"]
        transfer_calls += 1
        transferred = True
        return type("Proc", (), {"returncode": 0, "stderr": ""})()

    def fake_api(method, _endpoint, _payload=None):
        nonlocal patch_calls
        if method == "GET":
            return {"html_url": old_url, "node_id": "node-7", "number": 7}
        patch_calls += 1
        if patch_calls == 1:
            raise RuntimeError("interrupted after transfer")
        return {"url": new_url}

    monkeypatch.setattr(central, "resolve_node_url", fake_resolve)
    monkeypatch.setattr(central, "run", fake_run)
    monkeypatch.setattr(central, "api_json", fake_api)

    with pytest.raises(RuntimeError, match="interrupted"):
        central.transfer_issue(action, central.TARGET_REPOSITORY, state_path, state)
    recovered_state = json.loads(state_path.read_text())
    central.transfer_issue(
        action, central.TARGET_REPOSITORY, state_path, recovered_state
    )

    assert transfer_calls == 1
    assert patch_calls == 2
    assert recovered_state["transfers"][old_url]["new_url"] == new_url


def test_cross_rig_transfer_fails_before_remote_mutation(monkeypatch, tmp_path):
    central = load_script("github_central_cross_rig_test", "github-central-mirror")
    from github_mirror_common import RigSpec

    action = {
        "kind": "transfer",
        "spec": RigSpec("codeprobe", "/home/ds/projects/codeprobe"),
        "bead": {
            "id": "codeprobe-wrong",
            "external_ref": "https://github.com/sjarmak/mem-beads/issues/7",
        },
        "desired": {"title": "Wrong", "body": "body", "state": "open", "labels": []},
        "remote": None,
    }
    mutations = []

    monkeypatch.setenv("MIRROR_MAX_TRANSFER", "1")
    monkeypatch.setattr(central, "MIGRATION_STATE_PATH", tmp_path / "state.json")
    monkeypatch.setattr(central, "preflight_target_repository", lambda _repo: None)
    monkeypatch.setattr(central, "preflight_private_repository", lambda _repo: None)
    monkeypatch.setattr(
        central, "create_labels", lambda _repo, _labels: mutations.append("labels")
    )
    monkeypatch.setattr(
        central,
        "api_json",
        lambda method, *_args, **_kwargs: mutations.append(method),
    )

    with pytest.raises(RuntimeError, match="belongs to rig mem"):
        central.apply_plan(central.TARGET_REPOSITORY, [action], set())
    assert mutations == []


def test_corrupt_transfer_node_binding_fails_before_remote_mutation(monkeypatch, tmp_path):
    central = load_script("github_central_corrupt_binding_test", "github-central-mirror")
    from github_mirror_common import RigSpec

    old_url = "https://github.com/sjarmak/mem-beads/issues/7"
    action = {
        "kind": "transfer",
        "spec": RigSpec("mem", "/home/ds/projects/mem"),
        "bead": {"id": "mem-7", "external_ref": old_url},
        "desired": {"title": "Wrong node", "body": "body", "state": "open", "labels": []},
        "remote": None,
    }
    state_path = tmp_path / "state.json"
    state_path.write_text(
        json.dumps(
            {
                "transfers": {
                    old_url: {
                        "binding": "not-the-node-binding",
                        "new_url": None,
                        "node_id": "wrong-node",
                        "source_url": old_url,
                    }
                }
            }
        )
    )
    mutations = []

    monkeypatch.setenv("MIRROR_MAX_TRANSFER", "1")
    monkeypatch.setattr(central, "MIGRATION_STATE_PATH", state_path)
    monkeypatch.setattr(central, "preflight_target_repository", lambda _repo: None)
    monkeypatch.setattr(central, "preflight_private_repository", lambda _repo: None)
    monkeypatch.setattr(
        central, "create_labels", lambda _repo, _labels: mutations.append("labels")
    )

    with pytest.raises(RuntimeError, match="binding mismatch"):
        central.apply_plan(central.TARGET_REPOSITORY, [action], set())
    assert mutations == []


def test_runtime_handoff_requires_disabled_order(monkeypatch):
    central = load_script("github_central_order_gate_test", "github-central-mirror")

    monkeypatch.setattr(
        central,
        "run",
        lambda *_args, **_kwargs: type(
            "Proc",
            (),
            {
                "returncode": 0,
                "stdout": json.dumps(
                    {"orders": [{"name": "github-mirror", "enabled": True}]}
                ),
                "stderr": "",
            },
        )(),
    )

    with pytest.raises(RuntimeError, match="still enabled"):
        central.require_legacy_order_disabled()


def test_legacy_cycle_and_central_transfer_share_the_same_lock():
    central = load_script("github_central_shared_lock_test", "github-central-mirror")
    cycle = (BIN / "github-mirror-cycle").read_text()

    assert str(central.PILOT_LOCK_PATH) in cycle
    assert "flock -n 9" in cycle


def test_legacy_scripts_explicitly_refuse_the_central_view():
    for filename in ("github-mirror", "github-mirror-reconcile"):
        source = (BIN / filename).read_text()
        assert 'CENTRAL_VIEW_REPOSITORY = "sjarmak/gas-city-beads"' in source
        assert "== CENTRAL_VIEW_REPOSITORY" in source


def test_unknown_remote_marker_rig_fails_before_bead_reads(monkeypatch):
    central = load_script("github_central_unknown_rig_test", "github-central-mirror")
    from github_mirror_common import RigSpec, render_central_issue

    desired = render_central_issue(
        RigSpec("old-name", "/tmp/old"),
        {
            "id": "old-1",
            "title": "Old identity",
            "description": "",
            "issue_type": "task",
            "priority": 2,
            "status": "open",
        },
    )
    remote = {
        "number": 1,
        "url": "https://github.com/sjarmak/gas-city-beads/issues/1",
        **desired,
    }

    monkeypatch.setattr(central, "preflight_target_repository", lambda _repo: None)
    monkeypatch.setattr(central, "list_remote_issues", lambda _repo: [remote])
    monkeypatch.setattr(
        central,
        "load_records",
        lambda *_args: (_ for _ in ()).throw(AssertionError("beads were read")),
    )

    with pytest.raises(RuntimeError, match="missing from MIRROR_RIGS"):
        central.run_cycle(
            [RigSpec("new-name", "/tmp/new")],
            central.TARGET_REPOSITORY,
            "--dry-run",
            central.LEGACY_TRANSFER_REPOSITORIES,
        )


def test_completed_transfer_state_permanently_fences_legacy_source(tmp_path):
    from github_mirror_common import repositories_with_completed_transfers

    old_url = "https://github.com/sjarmak/mem-beads/issues/7"
    state = {
        "transfers": {
            old_url: {
                "binding": "binding",
                "new_url": "https://github.com/sjarmak/gas-city-beads/issues/12",
                "node_id": "node-7",
                "source_url": old_url,
            }
        }
    }
    path = tmp_path / "state.json"
    path.write_text(json.dumps(state))

    assert repositories_with_completed_transfers(path) == {"sjarmak/mem-beads"}
    for filename in ("github-mirror", "github-mirror-reconcile"):
        source = (BIN / filename).read_text()
        assert "repositories_with_completed_transfers" in source
        assert "permanently fenced" in source


def test_central_bd_reader_rejects_mutating_commands(monkeypatch):
    central = load_script("github_central_bd_readonly_test", "github-central-mirror")
    from github_mirror_common import RigSpec

    monkeypatch.setattr(
        central,
        "run",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(AssertionError("bd was called")),
    )

    with pytest.raises(RuntimeError, match="read-only"):
        central.bd_json(RigSpec("mem", "/home/ds/projects/mem"), ["update", "mem-1"])


def test_malformed_remote_issue_and_state_fail_closed():
    central = load_script("github_central_remote_validation_test", "github-central-mirror")

    with pytest.raises(RuntimeError, match="unexpected state"):
        central.validate_remote_issues(
            central.TARGET_REPOSITORY,
            [{"number": 1, "url": "https://github.com/sjarmak/gas-city-beads/issues/1", "title": "x", "body": "", "state": "merged", "labels": []}],
        )
    with pytest.raises(RuntimeError, match="outside target"):
        central.validate_remote_issues(
            central.TARGET_REPOSITORY,
            [{"number": 1, "url": "https://github.com/other/repo/issues/1", "title": "x", "body": "", "state": "open", "labels": []}],
        )
