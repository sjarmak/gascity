"""Tests for role/capacity-aware dispatch harness selection.

Run with: python3 -m pytest bin/test_gc_harness_select.py
"""

from __future__ import annotations

import importlib.machinery
import importlib.util
import math
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest


_SCRIPT = Path(__file__).resolve().parent / "gc-harness-select"


def _load_module():
    loader = importlib.machinery.SourceFileLoader(
        "harness_select_under_test", str(_SCRIPT)
    )
    spec = importlib.util.spec_from_loader("harness_select_under_test", loader)
    module = importlib.util.module_from_spec(spec)
    sys.modules["harness_select_under_test"] = module
    loader.exec_module(module)
    return module


selector = _load_module()


NOW = datetime(2026, 7, 23, 2, 0, tzinfo=timezone.utc)


def candidate(
    provider: str,
    family: str,
    roles: tuple[str, ...],
    *,
    maximum: int = 2,
    retired: bool = False,
) -> "selector.Candidate":
    return selector.Candidate(
        provider=provider,
        family=family,
        roles=roles,
        telemetry="test",
        max_active=maximum,
        retired=retired,
    )


def telemetry(
    used: float | None,
    *,
    age_minutes: int = 1,
    healthy: bool = True,
    bounded: bool = False,
) -> "selector.Telemetry":
    return selector.Telemetry(
        healthy=healthy,
        used_percent=used,
        observed_at=NOW - timedelta(minutes=age_minutes),
        source="test",
        bounded_fallback=bounded,
        detail="fixture",
    )


def policy(*candidates: "selector.Candidate") -> "selector.Policy":
    return selector.Policy(
        drain_percent=75.0,
        telemetry_max_age_seconds=30 * 60,
        role_max_active={
            "coordination": 3,
            "implementation": 4,
            "verification": 4,
        },
        candidates=candidates,
    )


def select(
    cfg: "selector.Policy",
    role: str,
    telemetry_by_provider: dict[str, "selector.Telemetry"],
    *,
    active: dict[str, int] | None = None,
    implementation_family: str = "",
    provider_pin: str = "",
) -> "selector.Selection":
    configured = {c.provider for c in cfg.candidates}
    targets = {c.provider: f"gascity/{c.provider}" for c in cfg.candidates}
    return selector.select_harness(
        policy=cfg,
        role=role,
        rig="gascity",
        telemetry_by_provider=telemetry_by_provider,
        active_by_provider=active or {},
        configured_providers=configured,
        targets_by_provider=targets,
        implementation_family=implementation_family,
        provider_pin=provider_pin,
        now=NOW,
    )


def test_implementation_balances_headroom_and_live_load() -> None:
    cfg = policy(
        candidate("codex", "openai", ("implementation",)),
        candidate("codex-2", "openai", ("implementation",)),
    )
    usage = {"codex": telemetry(15), "codex-2": telemetry(0)}

    first = select(cfg, "implementation", usage)
    assert first.provider == "codex-2"

    # One live codex-2 session makes the unused codex account the fairer next
    # allocation even though codex-2 still has slightly more quota headroom.
    second = select(cfg, "implementation", usage, active={"codex-2": 1})
    assert second.provider == "codex"


def test_city_policy_reserves_implementation_for_codex_accounts() -> None:
    cfg = selector.load_policy(selector.DEFAULT_POLICY)

    implementation = {
        candidate.provider
        for candidate in cfg.candidates
        if "implementation" in candidate.roles
    }

    assert implementation == {"codex", "codex-2"}


@pytest.mark.parametrize(
    ("provider", "reading", "reason"),
    [
        ("retired", telemetry(0), "retired"),
        ("down", telemetry(None, healthy=False), "down"),
        ("hot", telemetry(75), "drain line"),
        ("stale", telemetry(0, age_minutes=31), "stale"),
    ],
)
def test_unsafe_candidates_are_never_eligible(provider, reading, reason) -> None:
    cfg = policy(
        candidate(
            provider, "anthropic", ("implementation",), retired=provider == "retired"
        ),
    )

    with pytest.raises(selector.NoEligibleHarness) as exc:
        select(cfg, "implementation", {provider: reading})

    assert reason in str(exc.value)


def test_missing_telemetry_fails_closed_without_bounded_fallback() -> None:
    cfg = policy(candidate("codex", "openai", ("implementation",)))

    with pytest.raises(selector.NoEligibleHarness, match="telemetry unavailable"):
        select(cfg, "implementation", {})


def test_bounded_coordination_fallback_is_explicit_and_capped() -> None:
    cfg = policy(candidate("amp-medium", "amp", ("coordination",), maximum=2))
    bounded = {"amp-medium": telemetry(None, bounded=True)}

    chosen = select(cfg, "coordination", bounded, active={"amp-medium": 1})
    assert chosen.provider == "amp-medium"
    assert chosen.evidence["telemetry_bounded_fallback"] is True

    with pytest.raises(selector.NoEligibleHarness, match="provider ceiling"):
        select(cfg, "coordination", bounded, active={"amp-medium": 2})


def test_role_concurrency_ceiling_is_enforced_before_selection() -> None:
    cfg = policy(
        candidate("codex", "openai", ("implementation",), maximum=3),
        candidate("codex-2", "openai", ("implementation",), maximum=3),
    )
    usage = {"codex": telemetry(10), "codex-2": telemetry(10)}

    with pytest.raises(selector.NoEligibleHarness, match="role ceiling"):
        select(cfg, "implementation", usage, active={"codex": 2, "codex-2": 2})


def test_verification_prefers_a_different_model_family() -> None:
    cfg = policy(
        candidate("codex", "openai", ("implementation", "verification")),
        candidate("claude-5", "anthropic", ("verification",)),
    )
    usage = {"codex": telemetry(0), "claude-5": telemetry(50)}

    chosen = select(cfg, "verification", usage, implementation_family="openai")

    assert chosen.provider == "claude-5"
    assert chosen.independent_review is True


def test_verification_labels_same_family_fallback_non_independent() -> None:
    cfg = policy(
        candidate("codex", "openai", ("implementation", "verification")),
        candidate("codex-2", "openai", ("implementation", "verification")),
        candidate("claude-5", "anthropic", ("verification",)),
    )
    usage = {
        "codex": telemetry(10),
        "codex-2": telemetry(0),
        "claude-5": telemetry(84),
    }

    chosen = select(cfg, "verification", usage, implementation_family="openai")

    assert chosen.provider == "codex-2"
    assert chosen.independent_review is False
    assert "capacity diversity" in chosen.reason


def test_explicit_pin_is_authoritative_but_never_silently_substituted() -> None:
    cfg = policy(
        candidate("codex", "openai", ("implementation",)),
        candidate("codex-2", "openai", ("implementation",)),
    )
    usage = {"codex": telemetry(70), "codex-2": telemetry(0)}

    chosen = select(cfg, "implementation", usage, provider_pin="codex")
    assert chosen.provider == "codex"
    assert chosen.evidence["explicit_provider_pin"] is True

    with pytest.raises(selector.NoEligibleHarness, match="pinned provider codex"):
        select(
            cfg,
            "implementation",
            usage,
            active={"codex": 2},
            provider_pin="codex",
        )

    with pytest.raises(selector.NoEligibleHarness, match="drain line"):
        select(
            cfg,
            "implementation",
            {"codex": telemetry(75), "codex-2": telemetry(0)},
            provider_pin="codex",
        )


def test_explicit_pin_cannot_bypass_role_policy() -> None:
    cfg = policy(
        candidate("amp", "amp", ("coordination",)),
        candidate("codex", "openai", ("implementation",)),
    )
    usage = {"amp": telemetry(None, bounded=True), "codex": telemetry(0)}

    with pytest.raises(
        selector.NoEligibleHarness, match="role implementation not allowed"
    ):
        select(cfg, "implementation", usage, provider_pin="amp")


@pytest.mark.parametrize("used", [math.nan, math.inf, -1, 101])
def test_invalid_utilization_fails_closed(used: float) -> None:
    cfg = policy(candidate("codex", "openai", ("implementation",)))

    with pytest.raises(selector.NoEligibleHarness, match="invalid or unavailable"):
        select(cfg, "implementation", {"codex": telemetry(used)})


def test_verification_requires_implementation_family() -> None:
    cfg = policy(
        candidate("codex", "openai", ("implementation",)),
        candidate("claude-1", "anthropic", ("verification",)),
    )

    with pytest.raises(selector.HarnessSelectionError, match="implementation-family"):
        select(cfg, "verification", {"claude-1": telemetry(0)})

    with pytest.raises(
        selector.HarnessSelectionError, match="unknown implementation family"
    ):
        select(
            cfg,
            "verification",
            {"claude-1": telemetry(0)},
            implementation_family="typo",
        )

    with pytest.raises(
        selector.HarnessSelectionError, match="unknown implementation family"
    ):
        select(
            cfg,
            "verification",
            {"claude-1": telemetry(0)},
            implementation_family="amp",
        )


def test_missing_policy_is_a_structured_selection_error(tmp_path: Path) -> None:
    with pytest.raises(
        selector.HarnessSelectionError, match="cannot load harness policy"
    ):
        selector.load_policy(tmp_path / "missing.toml")


def test_unconfigured_provider_or_missing_target_is_ineligible() -> None:
    cfg = policy(candidate("codex", "openai", ("implementation",)))
    with pytest.raises(selector.NoEligibleHarness, match="not configured"):
        selector.select_harness(
            policy=cfg,
            role="implementation",
            rig="gascity",
            telemetry_by_provider={"codex": telemetry(0)},
            active_by_provider={},
            configured_providers=set(),
            targets_by_provider={},
            now=NOW,
        )


def test_latest_codex_rate_limit_reads_tail_without_crossing_accounts(
    tmp_path: Path,
) -> None:
    old = tmp_path / "sessions" / "old.jsonl"
    new = tmp_path / "sessions" / "new.jsonl"
    old.parent.mkdir()
    old.write_text(
        '{"timestamp":"2026-07-23T01:00:00Z","payload":{"rate_limits":{"primary":{"used_percent":70}}}}\n'
    )
    new.write_text(
        '{"timestamp":"2026-07-23T01:59:00Z","payload":{"rate_limits":{"primary":{"used_percent":12},"rate_limit_reached_type":null}}}\n'
    )
    old.touch()
    new.touch()

    reading = selector.load_codex_telemetry(tmp_path)

    assert reading.healthy is True
    assert reading.used_percent == 12
    assert reading.observed_at == datetime(2026, 7, 23, 1, 59, tzinfo=timezone.utc)


def test_invalid_codex_utilization_is_unhealthy(tmp_path: Path) -> None:
    event = tmp_path / "sessions" / "invalid.jsonl"
    event.parent.mkdir()
    event.write_text(
        '{"timestamp":"2026-07-23T01:59:00Z","payload":{"rate_limits":{"primary":{"used_percent":"NaN"}}}}\n'
    )

    reading = selector.load_codex_telemetry(tmp_path)

    assert reading.healthy is False
    assert reading.used_percent is None
    assert "invalid" in reading.detail


def test_claude_telemetry_uses_the_most_exhausted_enforced_window(
    tmp_path: Path,
) -> None:
    usage = tmp_path / "usage.json"
    usage.write_text(
        '{"accounts":[{"name":"account1","five_hour":{"utilization":80},'
        '"seven_day":{"utilization":10},"fetched_at":"2026-07-23T01:59:00Z",'
        '"error":null}]}'
    )

    reading = selector.load_claude_telemetry(usage)["account1"]

    assert reading.healthy is True
    assert reading.used_percent == 80
    cfg = policy(
        candidate("codex", "openai", ("implementation",)),
        candidate("claude-1", "anthropic", ("verification",)),
    )
    with pytest.raises(selector.NoEligibleHarness, match="drain line"):
        select(
            cfg,
            "verification",
            {"claude-1": reading},
            implementation_family="openai",
        )


def test_live_codex_rate_limit_query_refreshes_without_model_turn(
    tmp_path: Path,
) -> None:
    server = tmp_path / "fake-app-server.py"
    server.write_text(
        """#!/usr/bin/env python3
import json
import sys
for line in sys.stdin:
    request = json.loads(line)
    if request["id"] == 1:
        print(json.dumps({"id": 1, "result": {"codexHome": "fixture"}}), flush=True)
    elif request["id"] == 2:
        print(json.dumps({"id": 2, "result": {"rateLimits": {
            "primary": {"usedPercent": 7},
            "rateLimitReachedType": None,
            "spendControlReached": False
        }}}), flush=True)
"""
    )
    server.chmod(0o755)

    reading = selector.load_live_codex_telemetry(
        tmp_path,
        command=(sys.executable, str(server)),
        timeout=2,
    )

    assert reading.healthy is True
    assert reading.used_percent == 7
    assert reading.source == f"codex-app-server:{tmp_path}"
    assert reading.observed_at is not None


def test_runtime_inventory_excludes_suspended_targets(monkeypatch) -> None:
    responses = iter(
        [
            {"city_name": "ds-research", "rigs": []},
            {
                "agents": [
                    {
                        "qualified_name": "gascity/codex",
                        "provider": "codex",
                        "suspended": True,
                    },
                    {
                        "qualified_name": "gascity/codex-2",
                        "provider": "codex-2",
                        "suspended": False,
                    },
                ]
            },
            {"sessions": []},
        ]
    )
    monkeypatch.setattr(selector, "_run_json", lambda _command: next(responses))

    _active, targets, _rigs, _hq = selector._runtime_inventory()

    assert "gascity/codex" not in targets
    assert targets["gascity/codex-2"] == "codex-2"


def test_target_is_city_scoped_for_hq_and_rig_scoped_otherwise() -> None:
    assert selector.qualify_target("ds-research", "ds-research", "codex") == "codex"
    assert selector.qualify_target("gascity", "ds-research", "codex") == "gascity/codex"
