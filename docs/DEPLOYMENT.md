# Local deployment

## Bundle the runtime, keep weights external

A game ships its executable/PCK, the appropriate SaLMoN extension, and optional GGUF files. The extension statically links llama.cpp and Godot bindings in the default build. GPU/toolchain variants can still require system/driver/runtime libraries; inspect each artifact rather than assuming one file works everywhere.

Build separate Godot `template_debug` and `template_release` libraries. Include debug for editor/debug exports and release for release exports. The `.gdextension` names map to the CMake output. Windows x86_64, Linux x86_64/arm64 and macOS x86_64/arm64 are described; declaration is not evidence of validated binaries for each platform. Universal macOS packaging, signing, mobile, Web and console exports remain out of scope.

## Real filesystem paths

llama.cpp opens normal files. `ProjectSettings.globalize_path("res://...")` does **not** extract a file from a PCK. A GGUF visible through Godot's virtual filesystem may not be readable by llama.cpp.

Recommended options:

1. Distribute models alongside the exported game (or in a developer-chosen application-data directory) and pass an absolute path.
2. Copy a bundled resource from the PCK to `user://models/` using Godot file APIs before loading. This needs extra disk space and should not block rendering for large files.
3. Download a model with a developer-owned, consented, verified delivery process into `user://models/`. The separate `salmon-model` companion is the planned #7/#17 delivery path; networking will not be linked into the GDExtension.
4. Let players select a compatible local GGUF. Explain model/hardware/license requirements.

Example external desktop path:

```gdscript
var model_path = OS.get_executable_path().get_base_dir().path_join("models/chat.gguf")
var handle = ai.load_model(model_path, "chat")
```

Use a different development path in the editor: the editor executable directory is not the project directory. macOS app bundles/sandboxing also need platform-specific resource locations; do not assume the desktop example is universal.

## Model companion and installation records

The optional `salmon-model` companion queries Hugging Face live without persisting a catalog and is distributed separately from the Godot Asset Library addon. Only explicitly installed/prepared models and their minimal provenance are stored. See [the companion architecture](MODEL_COMPANION.md).

Pin installed model identity/revision, architecture, intended purpose, tokenizer/template, file size, SHA-256, source URL, license and attribution. Verify hashes before activation; use temporary downloads and atomic promotion. Support cancellation and corrupt-cache detection. Do not silently download weights on startup. Model weights are not bundled into runtime CI artifacts.

## Gameplay design

- Start with compact quantized models and measure on minimum-spec player hardware.
- Share a loaded chat handle between dialogue and decisions instead of duplicating weights.
- A node serializes inference. More nodes mean independent workers/models and greater CPU/RAM/VRAM pressure, not guaranteed throughput gains.
- Keep deterministic gameplay rules authoritative. An allowed option may still be the wrong choice; use fallbacks when a request fails or arrives too late.
- Cancel obsolete requests, bound prompt sizes, and schedule work outside frame-critical paths. Async alone does not make inference real-time.
- Benchmark CPU and GPU while the actual game is rendering. Aggressive inference offload can reduce frame rate.

## Distribution checklist

- Fresh editor import, debug/release export smoke tests and repeated load/unload on every target.
- Check dependent native libraries on a clean machine, not only a developer workstation.
- Include SaLMoN and dependency/model notices; confirm redistribution rights.
- Test missing/corrupt/incompatible models and low-memory conditions.
- Verify offline operation after installation.
- Resolve the known Windows first-import blocker (#11) before claiming production readiness.
