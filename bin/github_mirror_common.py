"""Shared contracts for the Gas City beads → GitHub Issues mirror."""

from __future__ import annotations

import json
import os
import re
import subprocess
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import urlparse


GITHUB_ISSUE_PATH = re.compile(r"^/([^/]+)/([^/]+)/issues/[1-9][0-9]*$")
WISP_META_KEYS = ("gc.step_ref", "gc.root_bead_id")
CENTRAL_MARKER_SENTINEL = "<!-- gas-city-bead "
CENTRAL_MARKER = re.compile(r"^<!-- gas-city-bead (\{[^\r\n]+\}) -->$", re.MULTILINE)
LEGACY_TRANSFER_REPOSITORIES = frozenset(
    {"sjarmak/mem-beads", "sjarmak/codeprobe-beads"}
)
PRIORITY_LABELS = {
    0: "critical",
    1: "high",
    2: "medium",
    3: "low",
    4: "none",
}


@dataclass(frozen=True)
class RigSpec:
    name: str
    path: str

    @property
    def label(self) -> str:
        return f"rig:{self.name}"


def parse_rig_specs(value: str) -> list[RigSpec]:
    """Parse `name=/path` tokens, retaining path-only pilot compatibility."""
    specs = []
    names = set()
    paths = set()
    for token in value.split():
        if "=" in token:
            name, path = token.split("=", 1)
        else:
            path = token
            name = os.path.basename(path.rstrip("/"))
        name = name.strip()
        path = os.path.abspath(path.strip())
        if not name or not path:
            raise ValueError(f"invalid rig token {token!r}")
        if name in names:
            raise ValueError(f"duplicate rig name {name!r}")
        if path in paths:
            raise ValueError(f"duplicate rig path {path!r}")
        names.add(name)
        paths.add(path)
        specs.append(RigSpec(name=name, path=path))
    return specs


def split_repository(repository: str) -> tuple[str, str]:
    parts = repository.strip().split("/")
    if len(parts) != 2 or not all(parts):
        raise ValueError(f"GitHub repository must be owner/name, got {repository!r}")
    return parts[0], parts[1]


def run(cmd, cwd="/", env=None, input_text=None):
    """One subprocess idiom for the whole mirror family."""
    return subprocess.run(
        cmd,
        cwd=cwd,
        env=env,
        input=input_text,
        capture_output=True,
        text=True,
    )


def rig_env(
    rig_path: str,
    base: dict[str, str] | None = None,
    repository: str | None = None,
) -> dict[str, str]:
    """bd must target the rig's store explicitly: the gc order controller pins
    BEADS_DIR to the CITY .beads in every exec env, which silently redirects
    cwd-based resolution to the city store (found 2026-07-19, first order fire)."""
    env = dict(base or os.environ)
    env["BEADS_DIR"] = os.path.join(rig_path, ".beads")
    if repository:
        env["BEADS_GITHUB_REPOSITORY"] = repository
    return env


def rig_environment(
    spec: RigSpec,
    base: dict[str, str] | None = None,
) -> dict[str, str]:
    return rig_env(spec.path, base)


def is_wisp(bead) -> bool:
    """The one wisp filter for every GitHub mirror leg. Formula wisps
    (gc.step_ref / gc.root_bead_id / gc.synthetic / gc.formula_name /
    gc.kind=workflow / type=convoy) must never reach GitHub; a divergence
    between copies of this filter leaks synthetic beads to the mirror,
    the family's own stated failure mode."""
    meta = bead.get("metadata") or {}
    if any(meta.get(key) for key in WISP_META_KEYS):
        return True
    if meta.get("gc.synthetic") == "true" or meta.get("gc.formula_name"):
        return True
    if meta.get("gc.kind") == "workflow":
        return True
    return bead.get("issue_type") == "convoy"


def bd_json(args, rig_path, base: dict[str, str] | None = None):
    """Run `bd <args> --json` against a rig's store and parse the result."""
    proc = run(["bd", *args, "--json"], cwd=rig_path, env=rig_env(rig_path, base))
    if proc.returncode != 0:
        raise RuntimeError(
            f"bd {' '.join(args)} failed in {rig_path}: {proc.stderr.strip()[:300]}"
        )
    try:
        return json.loads(proc.stdout or "[]")
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"bd returned malformed JSON in {rig_path}") from exc


def repository_from_issue_url(ref: str) -> str | None:
    if not ref:
        return None
    parsed = urlparse(ref)
    if parsed.scheme != "https" or parsed.netloc.lower() != "github.com":
        return None
    match = GITHUB_ISSUE_PATH.match(parsed.path.rstrip("/"))
    if not match:
        return None
    return f"{match.group(1)}/{match.group(2)}"


def repositories_with_completed_transfers(path) -> set[str]:
    """Read the durable cutover fence used by the legacy pilot scripts."""
    path = Path(path)
    if not path.exists():
        return set()
    try:
        state = json.loads(path.read_text())
        transfers = state["transfers"]
    except (json.JSONDecodeError, KeyError, TypeError) as exc:
        raise ValueError(f"malformed central migration state at {path}") from exc
    if not isinstance(transfers, dict):
        raise ValueError(f"malformed central migration state at {path}")
    completed = set()
    for old_url, entry in transfers.items():
        if not isinstance(entry, dict) or set(entry) != {
            "binding", "new_url", "node_id", "source_url"
        }:
            raise ValueError(f"malformed central migration state entry for {old_url!r}")
        if entry["new_url"] is None:
            continue
        repository = repository_from_issue_url(old_url)
        if repository not in LEGACY_TRANSFER_REPOSITORIES:
            raise ValueError(f"unapproved source in central migration state: {old_url!r}")
        completed.add(repository)
    return completed


def label_names(labels) -> list[str]:
    names = []
    for label in labels or []:
        if isinstance(label, str):
            names.append(label)
        elif isinstance(label, dict) and isinstance(label.get("name"), str):
            names.append(label["name"])
    return names


def flatten_issue_pages(pages) -> list[dict]:
    """Flatten `gh api --paginate --slurp` output and normalize issue fields."""
    issues = []
    for page in pages or []:
        for issue in page or []:
            if issue.get("pull_request"):
                continue
            normalized = dict(issue)
            normalized["url"] = issue.get("html_url", issue.get("url", ""))
            normalized["updatedAt"] = issue.get("updated_at", issue.get("updatedAt", ""))
            normalized["body"] = issue.get("body") or ""
            normalized["labels"] = label_names(issue.get("labels"))
            issues.append(normalized)
    return issues


def parse_json_lines(output: str) -> list[dict]:
    """Parse `gh api --paginate --jq '.[] | @json'` portable NDJSON."""
    return [json.loads(line) for line in output.splitlines() if line.strip()]


def central_marker(spec: RigSpec, bead_id: str) -> str:
    payload = json.dumps(
        {"id": bead_id, "rig": spec.name},
        sort_keys=True,
        separators=(",", ":"),
    )
    return f"<!-- gas-city-bead {payload} -->"


def marker_key(body: str) -> tuple[str, str] | None:
    body = body or ""
    if CENTRAL_MARKER_SENTINEL not in body:
        return None
    matches = list(CENTRAL_MARKER.finditer(body))
    if len(matches) != 1:
        raise ValueError(
            f"central mirror body must contain exactly one valid marker; found {len(matches)}"
        )
    match = matches[0]
    if match.end() != len(body.rstrip("\r\n")):
        raise ValueError("central mirror marker must be the final line")
    try:
        payload = json.loads(match.group(1))
    except json.JSONDecodeError as exc:
        raise ValueError("central mirror marker contains invalid JSON") from exc
    if not isinstance(payload, dict) or set(payload) != {"id", "rig"}:
        raise ValueError("central mirror marker must contain only id and rig")
    rig = payload.get("rig")
    bead_id = payload.get("id")
    if not isinstance(rig, str) or not rig or not isinstance(bead_id, str) or not bead_id:
        raise ValueError("central mirror marker id and rig must be non-empty strings")
    canonical = json.dumps(payload, sort_keys=True, separators=(",", ":"))
    if match.group(0) != f"{CENTRAL_MARKER_SENTINEL}{canonical} -->":
        raise ValueError("central mirror marker must use canonical encoding")
    return rig, bead_id


def render_central_issue(spec: RigSpec, bead: dict) -> dict:
    status = str(bead.get("status") or "open")
    issue_type = str(bead.get("issue_type") or "task")
    try:
        priority = int(bead.get("priority", 2))
    except (TypeError, ValueError):
        priority = 2
    labels = [
        spec.label,
        f"type::{issue_type}",
        f"priority::{PRIORITY_LABELS.get(priority, 'medium')}",
        f"status::{status}",
    ]
    provenance = (
        f"_Bead: `{bead['id']}` · Rig: `{spec.name}` · "
        "Beads is the canonical source._"
    )
    body_parts = []
    description = str(bead.get("description") or "").rstrip()
    if description:
        body_parts.append(description)
    body_parts.extend(["---\n" + provenance, central_marker(spec, bead["id"])])
    return {
        "title": str(bead.get("title") or bead["id"]),
        "body": "\n\n".join(body_parts) + "\n",
        "state": "closed" if status == "closed" else "open",
        "labels": labels,
    }


def central_issue_matches(remote: dict, desired: dict) -> bool:
    return (
        remote.get("title") == desired["title"]
        and remote.get("body", "") == desired["body"]
        and str(remote.get("state", "")).lower() == desired["state"]
        and sorted(label_names(remote.get("labels"))) == sorted(desired["labels"])
    )


def validate_central_issue(desired: dict) -> list[str]:
    errors = []
    if len(desired["title"]) > 256:
        errors.append(f"title exceeds 256 characters ({len(desired['title'])})")
    body_bytes = len(desired["body"].encode("utf-8"))
    if body_bytes > 65_536:
        errors.append(f"body exceeds 65536 UTF-8 bytes ({body_bytes})")
    if len(desired["labels"]) > 100:
        errors.append(f"labels exceed 100 ({len(desired['labels'])})")
    for label in desired["labels"]:
        if not isinstance(label, str) or not label:
            errors.append("label names must be non-empty strings")
            continue
        if len(label) > 50:
            errors.append(f"label exceeds 50 characters: {label!r}")
        if any(ord(character) < 32 or ord(character) == 127 for character in label):
            errors.append(f"label contains control characters: {label!r}")
    try:
        if marker_key(desired["body"]) is None:
            errors.append("body is missing the central mirror marker")
    except ValueError as exc:
        errors.append(f"invalid central mirror marker: {exc}")
    return errors


def build_central_plan(
    records: list[tuple[RigSpec, dict]],
    remote_issues: list[dict],
    legacy_repositories: set[str],
) -> list[dict]:
    """Plan by stable marker; external_ref is read only and never repurposed."""
    remote_by_key = {}
    for issue in remote_issues:
        key = marker_key(issue.get("body") or "")
        if key is None:
            continue
        if key in remote_by_key:
            raise ValueError(f"duplicate central mirror marker for {key[0]}:{key[1]}")
        remote_by_key[key] = issue

    actions = []
    record_keys = set()
    legacy_sources = {}
    for spec, bead in records:
        key = (spec.name, bead["id"])
        if key in record_keys:
            raise ValueError(f"duplicate bead identity for {key[0]}:{key[1]}")
        record_keys.add(key)
        external_ref = bead.get("external_ref") or ""
        ref_repo = repository_from_issue_url(external_ref)
        if ref_repo in LEGACY_TRANSFER_REPOSITORIES:
            previous = legacy_sources.get(external_ref)
            if previous is not None:
                raise ValueError(
                    f"duplicate legacy transfer source {external_ref} for "
                    f"{previous[0]}:{previous[1]} and {spec.name}:{bead['id']}"
                )
            legacy_sources[external_ref] = key
        desired = render_central_issue(spec, bead)
        remote = remote_by_key.get(key)
        validation_errors = validate_central_issue(desired)
        if validation_errors:
            kind = "invalid"
        elif remote is not None:
            kind = "noop" if central_issue_matches(remote, desired) else "update"
        else:
            kind = "transfer" if ref_repo in legacy_repositories else "create"
        actions.append(
            {
                "kind": kind,
                "spec": spec,
                "bead": bead,
                "desired": desired,
                "remote": remote,
                "error": "; ".join(validation_errors) if validation_errors else None,
            }
        )

    # While any pilot issue still needs transfer, new pilot-rig beads without
    # an external_ref must stay on the pilot only. Otherwise a central create
    # can race the live pilot push and leave two permanent representations.
    if any(action["kind"] == "transfer" for action in actions):
        pilot_rigs = {
            repository.rsplit("/", 1)[-1].removesuffix("-beads")
            for repository in legacy_repositories
        }
        for action in actions:
            if (
                action["kind"] == "create"
                and action["spec"].name in pilot_rigs
                and not action["bead"].get("external_ref")
            ):
                action["kind"] = "deferred"
    return actions
