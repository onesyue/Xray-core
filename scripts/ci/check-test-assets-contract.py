#!/usr/bin/env python3
"""Fail closed when the cross-platform test asset supply chain drifts."""

from pathlib import Path
import re
import sys


WORKFLOW = Path(__file__).parents[2] / ".github" / "workflows" / "test.yml"

EXPECTED_SNIPPETS = (
    "GEOSITE_URL: https://github.com/v2fly/domain-list-community/releases/download/20260813115850/dlc.dat",
    "GEOSITE_SHA256: b9a7877c99b2ab7580366d764e345789d6bbb8cb02579266346ada6e48070222",
    "GEOIP_URL: https://github.com/v2fly/geoip/releases/download/202601220433/geoip.dat",
    "GEOIP_SHA256: ed2de9add79623e2e5dbc5930ee39cc7037a7c6e0ecd58ba528b6f73d61457b5",
    "uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1",
    "uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1",
    "name: xray-test-assets-${{ github.sha }}",
    "needs: check-assets",
    "os: [windows-latest, ubuntu-latest, macos-latest]",
    "if-no-files-found: error",
    "sha256sum --check --strict",
    "Get-FileHash -Algorithm SHA256",
)

FORBIDDEN_PATTERNS = (
    (r"actions/cache(?:/restore)?@", "test assets must not depend on a cache"),
    (r"\bsleep\s+\d+", "missing assets must fail instead of waiting"),
    (r"v2fly/.+/releases/latest/", "release asset URLs must use immutable tags"),
    (r"Loyalsoldier/v2ray-rules-dat", "test assets must come from official v2fly repositories"),
)


def main() -> int:
    text = WORKFLOW.read_text(encoding="utf-8")
    errors = [
        f"missing required contract: {snippet}"
        for snippet in EXPECTED_SNIPPETS
        if snippet not in text
    ]

    for pattern, message in FORBIDDEN_PATTERNS:
        if re.search(pattern, text, flags=re.IGNORECASE):
            errors.append(message)

    external_actions = re.findall(r"uses:\s+([\w.-]+/[\w.-]+)@([^\s#]+)", text)
    for action, reference in external_actions:
        if not re.fullmatch(r"[0-9a-f]{40}", reference):
            errors.append(f"{action} is not pinned to a full commit SHA: {reference}")

    if errors:
        print("test asset contract failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print("test asset contract passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
