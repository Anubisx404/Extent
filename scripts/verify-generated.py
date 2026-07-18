#!/usr/bin/env python3
"""Run generated-project transaction verification without touching repository fixtures."""
from __future__ import annotations
import shutil, subprocess, sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TOOLS = ("go", "node", "npm", "python")

def main() -> int:
    print(f"repo: {ROOT}")
    for tool in TOOLS:
        path = shutil.which(tool)
        if path:
            try:
                version_args = ["version"] if tool == "go" else ["--version"]
                version = subprocess.run([tool, *version_args], text=True, capture_output=True, check=False)
                print(f"{tool}: available ({path}) {version.stdout.strip() or version.stderr.strip()}")
            except OSError as exc:
                print(f"{tool}: available ({path}), version error: {exc}")
        else:
            print(f"{tool}: unavailable")
    cmd = ["go", "test", "./internal/instrumenter", "-run", "^TestGeneratedProjectTransactions$", "-v"]
    print("command:", " ".join(cmd))
    result = subprocess.run(cmd, cwd=ROOT, text=True)
    return result.returncode

if __name__ == "__main__":
    sys.exit(main())
