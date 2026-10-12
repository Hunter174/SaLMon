"""Verify that a freshly staged SaLMon GDExtension survives Godot's first editor import.

Usage: python tests/godot/fresh_import.py GODOT_EXECUTABLE STAGED_ADDONS_DIRECTORY
Example: python tests/godot/fresh_import.py /path/to/godot build/native/addon/addons

This intentionally does NOT reuse tests/godot/.godot or retry an import: a second
successful run must never mask a failed first import. Only temporary files are used.
"""

import argparse
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("godot", type=Path)
    parser.add_argument("addons", type=Path, help="Directory containing salmon/salmon.gdextension and bin/")
    args = parser.parse_args()
    godot = args.godot.resolve()
    source = (args.addons.resolve() / "salmon")
    binary = source / "bin" / "salmon.windows.template_debug.x86_64.dll"
    if not godot.is_file() or not (source / "salmon.gdextension").is_file() or not binary.is_file():
        parser.error("Godot, staged .gdextension, and staged Windows debug DLL must exist")
    with tempfile.TemporaryDirectory(prefix="salmon-fresh-import-") as temp:
        project = Path(temp)
        (project / "project.godot").write_text('[application]\nconfig/name="SaLMon fresh import"\n', encoding="utf-8")
        shutil.copytree(source, project / "addons" / "salmon", ignore=shutil.ignore_patterns("~*"))
        command = [str(godot), "--headless", "--path", str(project), "--editor", "--import"]
        try:
            result = subprocess.run(command, capture_output=True, text=True, errors="replace", timeout=90)
        except subprocess.TimeoutExpired:
            print("First editor import timed out (90 s)", file=sys.stderr)
            return 1
        print(result.stdout)
        print(result.stderr, file=sys.stderr)
        print(f"First editor import exit code: {result.returncode}")
        return 0 if result.returncode == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
