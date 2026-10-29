#!/usr/bin/env python3
"""Regenerate licenses from Android package metadata, without an SDK/NDK."""
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]


def main():
    env = dict(os.environ, GOOS="android", GOARCH="arm64", CGO_ENABLED="1", GOTOOLCHAIN="local")
    raw = subprocess.check_output(["go", "list", "-deps", "-e", "-json", "."],
                                  cwd=ROOT / "apps/shared/fyne", env=env, text=True)
    decoder = json.JSONDecoder()
    modules = {}
    while raw.strip():
        package, end = decoder.raw_decode(raw.lstrip())
        raw = raw.lstrip()[end:]
        if package.get("Error"):
            raise SystemExit(f"Package metadata is incomplete: {package['ImportPath']}")
        module = package.get("Module")
        if module and not module.get("Main") and module["Path"] != "github.com/rectcircle/fn-connect-private-network":
            modules[module["Path"]] = module
    sections = ["# Third-party notices\n\nLicenses for dependencies selected by the Android arm64 package graph, including native Fyne Java sources. The project license does not replace these licenses.\n"]
    for name, module in sorted(modules.items()):
        directory = Path(module["Dir"])
        licenses = sorted(p for p in directory.iterdir() if p.is_file() and
                          p.name.lower().startswith(("license", "copying", "notice")))
        if not licenses:
            raise SystemExit(f"No cached license for {name}; refusing incomplete notices")
        sections.append(f"\n## {name} {module.get('Version', '')}\n")
        for source in licenses:
            sections.append(f"\n### {source.name}\n\n```text\n{source.read_text().rstrip()}\n```\n")
    (ROOT / "apps/shared/fyne/THIRD_PARTY_NOTICES.md").write_text("".join(sections))
    print(f"Recorded licenses for {len(modules)} Android dependency modules")


if __name__ == "__main__":
    main()
