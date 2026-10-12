"""Fail-closed Windows debug/release export gate; requires separate staged builds.

Usage: python tests/godot/export_smoke.py GODOT STAGED_DEBUG_DLL STAGED_RELEASE_DLL DEBUG_TEMPLATE RELEASE_TEMPLATE
All models are external; the test deliberately checks class registration and a model-free request only.
"""

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time


def run(command, label, timeout=90, cwd=None, env=None):
    try:
        result = subprocess.run(command, capture_output=True, text=True, errors="replace", timeout=timeout, cwd=cwd, env=env)
    except subprocess.TimeoutExpired:
        raise RuntimeError(f"{label}: timed out after {timeout}s") from None
    if result.returncode:
        raise RuntimeError(f"{label}: exit {result.returncode}\n{result.stdout}\n{result.stderr}")
    if label.endswith(" game") and "SALMON_EXPORT_SMOKE_PASSED" not in result.stdout:
        raise RuntimeError(f"{label}: missing success marker\n{result.stdout}\n{result.stderr}")
    print(f"{label}: PASSED")


class ExportTemporaryDirectory(tempfile.TemporaryDirectory):
    def cleanup(self):
        # Windows can hold an exported executable briefly after its process exits.
        for attempt in range(20):
            try:
                return super().cleanup()
            except PermissionError:
                if attempt == 19:
                    raise
                time.sleep(0.25)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("godot", "debug_dll", "release_dll", "debug_template", "release_template"):
        parser.add_argument(name, type=Path)
    parser.add_argument("--model", type=Path, help="Optional external chat GGUF for real-model exported-game checks")
    args = parser.parse_args()
    if args.model and not args.model.resolve().is_file():
        parser.error("--model must be an existing GGUF file outside the export")
    paths = [getattr(args, name).resolve() for name in ("godot", "debug_dll", "release_dll", "debug_template", "release_template")]
    if not all(path.is_file() for path in paths):
        parser.error("Godot, both DLLs, and both matching Windows templates must exist")
    godot, debug_dll, release_dll, debug_template, release_template = paths
    descriptor = Path(__file__).resolve().parents[2] / "addons" / "salmon" / "salmon.gdextension"
    scratch = Path(__file__).resolve().parents[2] / "build"
    scratch.mkdir(exist_ok=True)
    with ExportTemporaryDirectory(prefix="salmon-export-smoke-", dir=scratch) as temp:
        project = Path(temp)
        addon = project / "addons" / "salmon"
        (addon / "bin").mkdir(parents=True)
        shutil.copy2(descriptor, addon / descriptor.name)
        shutil.copy2(debug_dll, addon / "bin" / debug_dll.name)
        shutil.copy2(release_dll, addon / "bin" / release_dll.name)
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
            '    add_child(runtime)\n'
            '    var model_path := OS.get_environment("SALMON_CHAT_MODEL")\n'
            '    if not model_path.is_empty():\n'
            '        var done := {}\n        var failed := {}\n'
            '        runtime.request_completed.connect(func(id, value): done[id] = value)\n'
            '        runtime.request_failed.connect(func(id, error): failed[id] = error)\n'
            '        var load_id: int = runtime.load_model(model_path, "chat", {"context": 1024, "threads": 4})\n'
            '        var deadline := Time.get_ticks_msec() + 60000\n'
            '        while not done.has(load_id) and not failed.has(load_id) and Time.get_ticks_msec() < deadline:\n'
            '            await get_tree().process_frame\n'
            '        if not done.has(load_id):\n'
            '            push_error("Exported model load failed: " + str(failed.get(load_id, "timeout")))\n'
            '            get_tree().quit(1)\n            return\n'
            '        var model: int = done[load_id].model\n'
            '        var decision_id: int = runtime.decide(model, "The player is injured and has a healing potion.",'
            ' "Which action helps?", [{"id": "heal", "description": "Use the potion"},'
            ' {"id": "wait", "description": "Ignore the injury"}])\n'
            '        deadline = Time.get_ticks_msec() + 60000\n'
            '        while not done.has(decision_id) and not failed.has(decision_id) and Time.get_ticks_msec() < deadline:\n'
            '            await get_tree().process_frame\n'
            '        if not done.has(decision_id) or done[decision_id].scores.size() != 2:\n'
            '            push_error("Exported decision failed: " + str(failed.get(decision_id, "timeout/invalid scores")))\n'
            '            get_tree().quit(1)\n            return\n'
            '    runtime.queue_free()\n    print("SALMON_EXPORT_SMOKE_PASSED")\n    get_tree().quit(0)\n', encoding="utf-8"
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
        run([str(godot), "--headless", "--path", str(project), "--editor", "--import"], "fresh import", cwd=project)
        output_dir = project / "out"
        output_dir.mkdir()
        for target in ("debug", "release"):
            output = output_dir / f"salmon-{target}.exe"
            run([str(godot), "--headless", "--path", str(project), f"--export-{target}",
                 "Windows Desktop", str(output)], f"{target} export", cwd=project)
            # Godot places the exported DLL beside the exe, while the descriptor
            # still names res://addons/salmon/bin/. Ship it at the path it names.
            exported_dll = output_dir / (debug_dll.name if target == "debug" else release_dll.name)
            if not exported_dll.is_file():
                raise RuntimeError(f"{target} export: expected DLL missing: {exported_dll}")
            packaged_bin = output_dir / "addons" / "salmon" / "bin"
            packaged_bin.mkdir(parents=True, exist_ok=True)
            shutil.move(str(exported_dll), packaged_bin / exported_dll.name)
            environment = os.environ.copy()
            environment.pop("SALMON_CHAT_MODEL", None)
            if args.model:
                environment["SALMON_CHAT_MODEL"] = args.model.resolve().as_posix()
            run([str(output), "--headless"], f"{target} game", timeout=100 if args.model else 35,
                cwd=output_dir, env=environment)


if __name__ == "__main__":
    try:
        main()
    except RuntimeError as error:
        print(error, file=sys.stderr)
        sys.exit(1)
