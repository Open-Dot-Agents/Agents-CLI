#!/usr/bin/env python3
"""Generate and check compatibility claims from CLI/compatibility.json."""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any


CLI_ROOT = Path(__file__).resolve().parents[1]
REPO_ROOT = CLI_ROOT.parent
COMPATIBILITY_JSON = CLI_ROOT / "compatibility.json"
COMPATIBILITY_MD = REPO_ROOT / "docs" / "COMPATIBILITY.md"

START = "<!-- compatibility-table:start -->"
END = "<!-- compatibility-table:end -->"


def load_compatibility() -> dict[str, Any]:
    with COMPATIBILITY_JSON.open(encoding="utf-8") as file:
        data = json.load(file)
    if not isinstance(data, dict):
        raise ValueError("CLI/compatibility.json must contain an object")
    adapters = data.get("adapters")
    if not isinstance(adapters, list) or not adapters:
        raise ValueError("CLI/compatibility.json must define a non-empty adapters array")
    return data


def title_status(value: str) -> str:
    return value.replace("-", " ").capitalize()


def title_profile(value: str) -> str:
    words = value.split("-")
    return " ".join("CLI" if word == "cli" else word.capitalize() for word in words)


def render_table(data: dict[str, Any]) -> str:
    lines = [
        "| Adapter | Harness version | Instructions | Tools | Skills | Status | Evidence |",
        "| --- | --- | --- | --- | --- | --- | --- |",
    ]
    for adapter in data["adapters"]:
        profiles = adapter.get("profiles", {})
        lines.append(
            "| {name} | {version} | {instructions} | {tools} | {skills} | {status} | {evidence} |".format(
                name=adapter["name"],
                version=adapter.get("harness_version") or "Not pinned",
                instructions=title_profile(adapter.get("capabilities", {}).get("instructions", "unknown")),
                tools=title_profile(profiles.get("tools", "unknown")),
                skills=title_profile(profiles.get("skills", "unknown")),
                status=title_status(adapter["status"]),
                evidence=adapter["evidence"],
            )
        )
    return "\n".join(lines) + "\n"


def update_markdown(data: dict[str, Any]) -> bool:
    expected = f"{START}\n{render_table(data)}{END}"
    current = COMPATIBILITY_MD.read_text(encoding="utf-8")
    if START not in current or END not in current:
        raise ValueError(f"{COMPATIBILITY_MD} must contain {START} and {END} markers")
    prefix, rest = current.split(START, 1)
    _, suffix = rest.split(END, 1)
    updated = prefix + expected + suffix
    if updated == current:
        return False
    COMPATIBILITY_MD.write_text(updated, encoding="utf-8")
    return True


def check_markdown(data: dict[str, Any]) -> list[str]:
    expected = render_table(data)
    current = COMPATIBILITY_MD.read_text(encoding="utf-8")
    if START not in current or END not in current:
        return [f"{COMPATIBILITY_MD} is missing compatibility table markers"]
    table = current.split(START, 1)[1].split(END, 1)[0].strip() + "\n"
    if table != expected:
        return [f"{COMPATIBILITY_MD} is not generated from CLI/compatibility.json; run CLI/scripts/check_compatibility.py --write"]
    return []


def check_supported_evidence(data: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    for adapter in data["adapters"]:
        status = adapter.get("status")
        if status != "conformance-supported":
            continue
        label = adapter.get("id", adapter.get("name", "<unknown>"))
        if not adapter.get("harness_version"):
            errors.append(f"{label}: conformance-supported requires harness_version")
        capabilities = adapter.get("capabilities", {})
        required = {"instructions", "instructions.scoped", "skills", "mcp.stdio", "mcp.remote", "mcp.envRef"}
        if set(capabilities) != required:
            errors.append(f"{label}: conformance-supported must declare every 1.0 capability")
        for capability, outcome in capabilities.items():
            if outcome not in {"lossless", "transformed"}:
                errors.append(f"{label}: conformance-supported capability {capability} is {outcome}")
        evidence = str(adapter.get("evidence", "")).lower()
        if "unit test" in evidence or "projection test" in evidence or "no version-pinned" in evidence:
            errors.append(f"{label}: conformance-supported requires native black-box evidence")
    return errors


def run_cli_capabilities(vendor: str) -> dict[str, Any]:
    env = os.environ.copy()
    env.setdefault("GOCACHE", "/tmp/agents-gocache")
    env.setdefault("GOPATH", "/tmp/agents-gopath")
    completed = subprocess.run(
        ["go", "run", "./cmd/agents", "capabilities", "--vendor", vendor],
        cwd=CLI_ROOT,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"agents capabilities --vendor {vendor} failed: {completed.stderr.strip()}")
    return json.loads(completed.stdout)


def check_cli_capabilities(data: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    for adapter in data["adapters"]:
        vendor = adapter.get("reference_cli_vendor")
        if not vendor:
            continue
        actual = run_cli_capabilities(vendor)
        expected_profiles = adapter.get("profiles", {})
        comparisons = {
            "name": adapter.get("name"),
            "harness": adapter.get("harness"),
            "harness_version": adapter.get("harness_version"),
            "status": adapter.get("status"),
            "profile_status": expected_profiles,
            "capability_status": adapter.get("capabilities", {}),
            "evidence": adapter.get("evidence"),
            "limitations": adapter.get("limitations", []),
        }
        for key, expected in comparisons.items():
            actual_value = actual.get(key)
            if actual_value != expected:
                errors.append(f"{vendor}: CLI capabilities {key}={actual_value!r}, expected {expected!r}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--write", action="store_true", help="rewrite generated compatibility documentation")
    args = parser.parse_args()

    data = load_compatibility()
    if args.write:
        changed = update_markdown(data)
        if changed:
            print(f"updated {COMPATIBILITY_MD.relative_to(REPO_ROOT)}")

    errors = []
    errors.extend(check_markdown(data))
    errors.extend(check_supported_evidence(data))
    errors.extend(check_cli_capabilities(data))
    if errors:
        for error in errors:
            print(f"FAIL {error}", file=sys.stderr)
        return 1
    print("compatibility data, Markdown, CLI summaries, and support evidence rules agree")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
