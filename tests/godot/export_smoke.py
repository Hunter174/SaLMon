"""Fail-closed Windows debug/release export gate; requires separate staged builds.

Usage: python tests/godot/export_smoke.py GODOT STAGED_DEBUG_DLL STAGED_RELEASE_DLL DEBUG_TEMPLATE RELEASE_TEMPLATE
All models are external; the test deliberately checks class registration and a model-free request only.
"""

import argparse
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def run(command, label, timeout=90):
    try:
        result = subprocess.run(command, capture_output=True, text=True, errors="replace", timeout=timeout)
    except subprocess.TimeoutExpired:
        raise RuntimeError(f"{label}: timed out after {timeout}s") from None
    if result.returncode:
        raise RuntimeError(f"{label}: exit {result.returncode}\n{result.stdout}\n{result.stderr}")
    if label.endswith(" game") and "SALMON_EXPORT_SMOKE_PASSED" not in result.stdout:
        raise RuntimeError(f"{label}: missing success marker\n{result.stdout}\n{result.stderr}")
    print(f"{label}: PASSED")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("godot", "debug_dll", "release_dll", "debug_template", "release_template"):
        parser.add_argument(name, type=Path)
    args = parser.parse_args()
    paths = [getattr(args, name).resolve() for name in ("godot", "debug_dll", "release_dll", "debug_template", "release_template")]
    if not all(path.is_file() for path in paths):
        parser.error("Godot, both DLLs, and both matching Windows templates must exist")
    godot, debug_dll, release_dll, debug_template, release_template = paths
    descriptor = Path(__file__).resolve().parents[2] / "addons" / "salmon" / "salmon.gdextension"
    scratch = Path(__file__).resolve().parents[2] / "build"
    scratch.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="salmon-export-smoke-", dir=scratch) as temp:
        project = Path(temp)
        addon = project / "addons" / "salmon"
        (addon / "bin").mkdir(parents=True)
        shutil.copy2(descriptor, addon / descriptor.name)
        shutil.copy2(debug_dll, addon / debug_dll.name)
        shutil.copy2(release_dll, addon / release_dll.name)
        (project / "project.godot").write_text(
            '[application]\nconfig/name="SaLMon export smoke"\nrun/main_scene="res://probe.tscn"\n', encoding="utf-8"
        )
        (project / "probe.tscn").write_text(
            '[gd_scene load_steps=2 format=3]\n[ext_resource type="Script" path="res://probe.gd" id="1"]\n'
            '[node name="Probe" type="Node"]\nscript = ExtResource("1")\n', encoding="utf-8"
        )
        (project / "probe.gd").write_text(
            'extends Node\nfunc _ready():\n'
            '    var valid := ClassDB.class_exists("Salmon") and ClassDB.get_parent_class("Salmon") == "Node"'
            ' and ClassDB.class_has_method("Salmon", "decide_many")\n'
            '    if not valid:\n        push_error("Salmon extension class is not a usable Node with decide_many")\n'
            '        get_tree().quit(1)\n        return\n'
            '    var runtime = ClassDB.instantiate("Salmon")\n'
            '    if not runtime is Node:\n        push_error("Salmon did not instantiate as a Node")\n'
            '        get_tree().quit(1)\n        return\n'
            '    add_child(runtime)\n    runtime.queue_free()\n'
            '    print("SALMON_EXPORT_SMOKE_PASSED")\n    get_tree().quit(0)\n', encoding="utf-8"
        )
        def godot_path(path):
            return path.as_posix()
        (project / "export_presets.cfg").write_text(
            '[preset.0]\nname="Windows Desktop"\nplatform="Windows Desktop"\nrunnable=true\n'
            'dedicated_server=false\ncustom_features=""\nexport_filter="all_resources"\n'
            'include_filter=""\nexclude_filter=""\nexport_path=""\n'
            'encrypt_pck=false\nencrypt_directory=false\n[preset.0.options]\n'
            f'custom_template/debug="{godot_path(debug_template)}"\n'
            f'custom_template/release="{godot_path(release_template)}"\n'
            'binary_format/architecture="x86_64"\napplication/modify_resources=false\n', encoding="utf-8"
        )
        run([str(godot), "--headless", "--path", str(project), "--editor", "--import"], "fresh import")
        output_dir = project / "out"
        output_dir.mkdir()
        for target in ("debug", "release"):
            output = output_dir / f"salmon-{target}.exe"
            run([str(godot), "--headless", "--path", str(project), f"--export-{target}",
                 "Windows Desktop", str(output)], f"{target} export")
            run([str(output), "--headless"], f"{target} game", timeout=35)


if __name__ == "__main__":
    try:
        main()
    except RuntimeError as error:
        print(error, file=sys.stderr)
        sys.exit(1)
