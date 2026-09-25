# Validation status

This is an implementation preview, not a certified game-distribution release.

## Locally exercised

Windows x86_64, MinGW GCC 13.1 and MSVC 19.44, CMake, portable/native CPU builds, Godot 4.5.1 stable official:

- Extension built from source with llama.cpp `bf78f5439ee8e82e367674043303ebf8e92b4805` and godot-cpp `d502d8e8aae35248bad69b9f40b98150ab694774`.
- Updated the parent llama.cpp gitlink to the tested revision. Preserved pre-existing local submodule edits outside the commit.
- Rebuilt native runtime/tests from a **clean git archive** of that llama.cpp revision using `SALMON_LLAMA_SOURCE_DIR`; native tests and both real-model smokes pass without the local patch. CMake supplies the narrow MinGW thread-API compatibility workaround instead of editing dependency sources.
- Native CTest suite: conditional softmax and invalid inputs; option validation; UTF-8 chunk boundaries; FIFO delivery; stream preservation; worker exceptions; active/queued cancellation; backpressure; outstanding request retirement; active-worker destruction; invalid model files and handles.
- Real-model native smoke: local SmolLM2-135M-Instruct GGUF — chat, stream equality, repeated-request context isolation, decision-score shape/normalization.
- Real-model native smoke: local Qwen3-0.6B Q4_K_M GGUF — chat, stream equality, repeated-request context isolation, and decision-score shape/normalization.
- Standalone model companion: Windows local tests plus three-platform Go CI cover live Hugging Face search/inspection/planning, safe identifier handling, exact-plan consent, bounded download, cancellation cleanup, SHA-256 verification, GGUF header rejection, atomic managed installation, listing, and removal. The browser UI additionally tests loopback API authorization, embedded assets/CSP, unrestricted search, auditable recommendation metadata, provider-scoped live search, purpose/size/license/quantization filtering, conservative target-hardware fit labels, pinned-converter architecture-matrix drift, non-GGUF source assessment, exact toolchain and quantization consent, safe ZIP extraction, executable tamper detection, atomic toolchain install/removal, quantized-output cleanup/validation/registration, pinned conversion-environment consent/extraction/cleanup, planning, asynchronous install progress, verified completion, and portable project-manifest assignment. Manifest tests cover deterministic purpose handling, replacement by export destination, removal, transactional rewrites, and unsafe-path rejection. A live 21,273,408-byte `groonga/all-MiniLM-L6-v2-Q4_K_M-GGUF` install resolved commit `34d07f4f6a28cdd1795cea0a5cdb82f44dc20e94`, verified SHA-256 `11b47b18c61abb8af542019c6588edf75b0794d952594a7ed21cd0760d09b8fb`, then removed it successfully. The pinned Windows `llama-quantize b6002` archive was also consented, downloaded, hash-verified, probed, listed, and removed. An end-to-end quantization used `ggml-org/models` commit `499bc8821c6b12b4e53c5bffcb21ec206f212d81`, file `tinyllamas/stories15M.gguf`, input SHA-256 `61b50d457809a5194818fd22e6724b456cd7bb9a6264c52c8110684c53f3704a` and produced a 20,986,208-byte Q4_K_M GGUF with SHA-256 `d1a262314de1c47dab44532d539f9ca4937ac1958409683f1697f5d6d2cc72dc`; the output passed managed structural validation and loaded through the native llama.cpp backend (the chat smoke then correctly rejected the source model's missing chat template). The Windows conversion environment then verified 69,003,524 bytes of pinned bootstrap archives, installed 27 hash-locked CPU dependencies into an isolated environment of 1,439,136,273 bytes, passed offline import and converter-help probes, listed successfully, and was removed. A subsequent Windows live `ModelCloud/tinyllama-15M-stories` conversion resolved commit `0234f07bde62afd22de0e68087b4f88e31ad5b29`, verified 32,233,995 source bytes including Git blob IDs for metadata and SHA-256 for Safetensors, and generated a 31,118,304-byte F16 GGUF with SHA-256 `f8a82e26d5d9a40516ac1368453016c6d0d6996baacefb59afe03789346ce131`. The converter uses a pinned, hardened `trust_remote_code=False` copy. The generated model passed structural GGUF validation and managed registration. A separate native smoke loaded its 57 tensors, then correctly rejected chat because the source has no chat template; the registry still reports `runtime_validation: not-run` because that probe is not the general runtime-validation workflow. This is structural delivery and preparation validation, not runtime/model-quality certification.
- Extensible performance harness (`salmon_performance_benchmark`): external JSON model/workload manifests; llama.cpp capability metadata; explicit unsupported-operation skips; cold load/first inference; warm p50/p90/p95; prompt throughput; chat time-to-first-token and generation throughput. The initial local v1 run exercised Qwen3 chat/decision and MiniLM embeddings from one manifest. See `docs/BENCHMARKING.md`.
- Initial semantic fixture report (`salmon_semantic_benchmark`) on the native-tuned i7-10750H build:
  - Qwen3-0.6B Q4_K_M: 13/16 expected choices (81.2%), 5/8 option-order stability (62.5%), 761 ms mean per trial.
  - Qwen3-0.6B Q8_0: 14/16 expected choices (87.5%), 6/8 option-order stability (75.0%), 809 ms mean per trial.
  - Q8 improved this small set at modest CPU cost, but both quantizations show material option-position bias and are **not yet certified for gameplay decisions**.
  - A single neutral label-prior calibration was rejected: it improved Q4 to 87.5%/75% but degraded Q8 to 62.5%/50%, showing the bias is not a stable model-independent constant.
  - Averaging logits across all four cyclic option positions produced 7/8 fixture accuracy for both quantizations and removes position dependence by construction, but cost about 2.60 s (Q4) or 2.95 s (Q8) per four-option decision. It still chose the wrong reaction for the threat fixture.
- Interactive `tests/godot/main.tscn` assembled with separate chat, semantic, and embeddings scenes; headless run with Qwen3 + MiniLM passes. Qwen weights remain ignored and are documented in `tests/models/README.md`.
- Real-model native smoke: local all-MiniLM-L6-v2-Q4_K_M GGUF — 384-dimensional normalized, repeatable embeddings.
- Embedding reference agreement (`salmon_embedding_reference`): six fixed texts compared against normalized FP32 mean-pooled `sentence-transformers/all-MiniLM-L6-v2` revision `1110a243fdf4706b3f48f1d95db1a4f5529b4d41`. The Q4 GGUF cosine matrix had 0.021439 maximum and 0.011113 mean off-diagonal delta, passing the documented 0.08 tolerance. Loading Qwen3 as an embedding model was rejected because it lacks supported sequence pooling.
- Headless Godot script: registration, errors, real chat/decision/embedding calls, stream/final equality, unload errors, queued-load destruction, compatibility class registration.
- Windows dependency inspection: generated MinGW CPU DLL imports ADVAPI32.dll, KERNEL32.dll and msvcrt.dll; no external MinGW runtime DLLs.
- Clean MSVC x64 Release build and CTest pass. All native/runtime/godot-cpp targets use the same static MSVC CRT; this fixes the mixed `/MD`/`/MT` linker failure first exposed by the GitHub Windows matrix.

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

Compare a local MiniLM GGUF against the committed canonical cosine fixtures:

```sh
build/dev/salmon_embedding_reference path/to/all-MiniLM-L6-v2.gguf
```

For Godot, copy `tests/godot/project.godot` and `tests/godot/smoke.gd` into a temporary project and copy the **freshly built** `build/dev/addon/addons/` directory into it. With multi-config generators, move the generated DLL from the `bin/Release/` subdirectory to `bin/` first.

```sh
godot --headless --path /path/to/test-project --editor --import
godot --headless --path /path/to/test-project --script smoke.gd
```

Record any import failure independently; it invalidates clean-import certification even if the runtime script passes afterward. Set `SALMON_CHAT_MODEL` and `SALMON_EMBEDDING_MODEL` to absolute GGUF paths to enable real-model checks in the script. Without them, the script tests model-free lifecycle/error handling.

## Not yet verified

- Linux/macOS builds (CI matrix added but not run from this checkout)
- Windows clean-machine installation (MSVC build itself is verified)
- Godot 4.4 and other engine/bindings combinations
- GPU compilation/runtime, device selection/fallback, memory pressure
- Exported games and clean installs
- Reference embedding agreement beyond norm/repeatability
- Decision accuracy/calibration across models and option order
- Quantization/CPU performance and rendering contention
- Long-context, large-output and stress/sanitizer testing

The GitHub workflow uploads model-free CPU **preview artifacts** after native tests. It deliberately does not publish releases or imply that native tests replace Godot/export validation.

Commit `2b9eb98` was additionally verified from a fresh recursive worktree: portable MinGW Release configure/build, CTest, explicit `salmon_stage_godot_test`, and model-free Godot 4.5.1 headless smoke all passed.
