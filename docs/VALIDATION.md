# Validation status

This is an implementation preview, not a certified game-distribution release.

## Locally exercised

Windows x86_64, MinGW GCC 13.1, CMake, portable CPU build, Godot 4.5.1 stable official:

- Extension built from source with llama.cpp `bf78f5439ee8e82e367674043303ebf8e92b4805` and godot-cpp `d502d8e8aae35248bad69b9f40b98150ab694774`.
- Updated the parent llama.cpp gitlink to the tested revision. Preserved pre-existing local submodule edits outside the commit.
- Rebuilt native runtime/tests from a **clean git archive** of that llama.cpp revision using `SALMON_LLAMA_SOURCE_DIR`; native tests and both real-model smokes pass without the local patch. CMake supplies the narrow MinGW thread-API compatibility workaround instead of editing dependency sources.
- Native CTest suite: conditional softmax and invalid inputs; option validation; UTF-8 chunk boundaries; FIFO delivery; stream preservation; worker exceptions; active/queued cancellation; backpressure; outstanding request retirement; active-worker destruction; invalid model files and handles.
- Real-model native smoke: local SmolLM2-135M-Instruct GGUF — chat, stream equality, repeated-request context isolation, decision-score shape/normalization.
- Real-model native smoke: local Qwen3-0.6B Q4_K_M GGUF — chat, stream equality, repeated-request context isolation, and decision-score shape/normalization.
- Initial semantic fixture report (`salmon_semantic_benchmark`) on the native-tuned i7-10750H build:
  - Qwen3-0.6B Q4_K_M: 13/16 expected choices (81.2%), 5/8 option-order stability (62.5%), 761 ms mean per trial.
  - Qwen3-0.6B Q8_0: 14/16 expected choices (87.5%), 6/8 option-order stability (75.0%), 809 ms mean per trial.
  - Q8 improved this small set at modest CPU cost, but both quantizations show material option-position bias and are **not yet certified for gameplay decisions**.
- Interactive `tests/godot/main.tscn` assembled with separate chat, semantic, and embeddings scenes; headless run with Qwen3 + MiniLM passes. Qwen weights remain ignored and are documented in `tests/models/README.md`.
- Real-model native smoke: local all-MiniLM-L6-v2-Q4_K_M GGUF — 384-dimensional normalized, repeatable embeddings.
- Headless Godot script: registration, errors, real chat/decision/embedding calls, stream/final equality, unload errors, queued-load destruction, compatibility class registration.
- Windows dependency inspection: generated CPU DLL imports ADVAPI32.dll, KERNEL32.dll and msvcrt.dll; no external MinGW runtime DLLs.

Test models were supplied locally for validation. No weights were committed or added to release artifacts. Smoke tests are structural/correctness checks, **not decision accuracy, quality or performance benchmarks**. The small chat model selected the wrong answer on a sample decision; do not use that model's smoke-test pass as a recommendation for gameplay decisions.

## Known failure: first editor import

A **fresh** project's `--headless --editor --import` crashes with signal 11 after editor layout initialization on this Windows/Godot 4.5.1 setup. This means a newly staged project may not build Godot's extension cache; the symptom in the editor is `Salmon` being an unknown node type, even though the binary itself works in a cached/headless runtime. Reproduced in multiple fresh projects with both the new DLL and the pre-refactor local DLL. An empty project imports normally. A subsequent import succeeds, and headless runtime scripts pass.

Tracked as [#11](https://github.com/Hunter174/SaLMon/issues/11). Root cause is not established. Do not hide this behind retries or publish a production-ready claim. Binding/engine version comparison and a minimal reproduction remain required.

## Reproduction

Build and run native tests:

```sh
cmake -S . -B build/dev -DCMAKE_BUILD_TYPE=Release -DGODOTCPP_TARGET=template_debug
cmake --build build/dev --config Release --parallel 4
ctest --test-dir build/dev -C Release --output-on-failure
```

Run the optional game-decision quality report against one or more local candidate models:

```sh
build/dev/salmon_semantic_benchmark path/to/model.gguf [path/to/second-model.gguf]
```

This reports accuracy, margin, latency, and option-order stability without imposing a universal pass threshold.

For Godot, copy `tests/godot/project.godot` and `tests/godot/smoke.gd` into a temporary project and copy the **freshly built** `build/dev/addon/addons/` directory into it. With multi-config generators, move the generated DLL from the `bin/Release/` subdirectory to `bin/` first.

```sh
godot --headless --path /path/to/test-project --editor --import
godot --headless --path /path/to/test-project --script smoke.gd
```

Record any import failure independently; it invalidates clean-import certification even if the runtime script passes afterward. Set `SALMON_CHAT_MODEL` and `SALMON_EMBEDDING_MODEL` to absolute GGUF paths to enable real-model checks in the script. Without them, the script tests model-free lifecycle/error handling.

## Not yet verified

- Linux/macOS builds (CI matrix added but not run from this checkout)
- Windows MSVC build and clean-machine installation
- Godot 4.4 and other engine/bindings combinations
- GPU compilation/runtime, device selection/fallback, memory pressure
- Exported games and clean installs
- Reference embedding agreement beyond norm/repeatability
- Decision accuracy/calibration across models and option order
- Quantization/CPU performance and rendering contention
- Long-context, large-output and stress/sanitizer testing

The GitHub workflow uploads model-free CPU **preview artifacts** after native tests. It deliberately does not publish releases or imply that native tests replace Godot/export validation.

Commit `2b9eb98` was additionally verified from a fresh recursive worktree: portable MinGW Release configure/build, CTest, explicit `salmon_stage_godot_test`, and model-free Godot 4.5.1 headless smoke all passed.
