# SaLMoN implementation roadmap

Local inference only: chat generation, semantic decisions, and embeddings. Model delivery (bundled/downloaded/user supplied) is separate from inference. GGUF weights stay outside the native library and must be real filesystem files, not PCK-only resources.

## Ordered work items

1. **Hygiene**: portable out-of-tree CMake, build dependencies from source, isolate native runtime from Godot, remove unsafe shell downloads and misleading release packaging, document migration.
2. **Lifecycle**: RAII model handles, owned worker, bounded queue, request IDs, cancellation, safe shutdown/reload, main-thread signals and structured errors.
3. **Chat**: one generation implementation, correctly sized templates/tokens, context limits, UTF-8 streaming, fresh state per request.
4. **Decisions**: experimental direct single-token label scoring, validated options, stable softmax, metadata and latency; no confidence/correctness guarantees.
5. **Embeddings**: persistent dedicated encoder handles, correct encoder/pooling path, normalization, multiple texts per request; cross-text batching later.
6. **CPU performance**: reproducible benchmarks, thread/batch/context/mmap controls; cache reuse only after correctness tests.
7. **Model delivery**: opt-in HTTPS downloads, pinned manifest/checksum/license, temporary files + atomic promotion, cancellation, size limits, export examples. No inference networking.
8. **GPU**: optional Vulkan/CUDA/Metal builds, device discovery, explicit selection and tested fallback. Never assume the rendering GPU has spare memory.
9. **Release validation**: platform matrix, headless Godot lifecycle/export tests, packaged dependencies/notices, checksums, no weights in runtime release.
10. **ONNX evaluation**: benchmark embeddings against llama.cpp before adding a second runtime.

## Constraints

- Preserve existing local edits and binaries; do not publish releases automatically.
- CPU is the default. Avoid host-specific instruction tuning in redistributable builds.
- Async does not mean real-time: measure tail latency and frame-time impact.
- Direct logits require token labels validated for the exact prompt boundary/tokenizer. Arbitrary multi-token option descriptions are not scored as single tokens.
- Separate questions require separate suffix evaluation; shared prefixes are not free batching.
- Type-safe output does not imply a semantically correct choice. Scores are conditional on supplied options and uncalibrated.
- Model architecture, GGUF compatibility, pooling and licenses must be validated per model.
- Do not claim current support for newer models merely because upstream llama.cpp supports them; the submodule is pinned.

## Initial implementation boundary

Implement items 1–5 and configurable CPU/GPU build foundations first. Keep downloads, caching/batching optimization, device auto-selection, GPU benchmarks, production release validation and ONNX as explicit follow-ups. The public API proposed in conversation was illustrative, not an existing compatibility contract.

## GitHub backlog

- [Hygiene: portable builds and runtime separation](https://github.com/Hunter174/SaLMon/issues/1)
- [Runtime: owned worker, model handles and cancellation](https://github.com/Hunter174/SaLMon/issues/2)
- [Chat: consolidate generation and validate prompt boundaries](https://github.com/Hunter174/SaLMon/issues/3)
- [Semantic decisions: native direct-logit scoring](https://github.com/Hunter174/SaLMon/issues/4)
- [Embeddings: persistent encoder models and correct pooling](https://github.com/Hunter174/SaLMon/issues/5)
- [CPU performance: benchmarks, batching and prefix reuse](https://github.com/Hunter174/SaLMon/issues/6)
- [Models: bundled exports and opt-in verified download cache](https://github.com/Hunter174/SaLMon/issues/7)
- [GPU: Vulkan, CUDA and Metal support with tested fallback](https://github.com/Hunter174/SaLMon/issues/8)
- [Release engineering: platform builds and Godot integration tests](https://github.com/Hunter174/SaLMon/issues/9)
- [Evaluate ONNX for embeddings only](https://github.com/Hunter174/SaLMon/issues/10)
- [Windows first-import release blocker](https://github.com/Hunter174/SaLMon/issues/11)
- [Interactive three-domain Godot project](https://github.com/Hunter174/SaLMon/issues/12)
- [Hybrid natural-language NPC reference](https://github.com/Hunter174/SaLMon/issues/13)
- [Grammar-constrained game tool calls](https://github.com/Hunter174/SaLMon/issues/14)
- [Bounded local agent/tool loop](https://github.com/Hunter174/SaLMon/issues/15)
- [Scoped persistent NPC memory](https://github.com/Hunter174/SaLMon/issues/16)
- [Live Hugging Face model discovery companion](https://github.com/Hunter174/SaLMon/issues/17)
- [Optional GGUF conversion and quantization tooling](https://github.com/Hunter174/SaLMon/issues/18)
- [Discovery-to-preparation compatibility planning](https://github.com/Hunter174/SaLMon/issues/19)

## Issue re-evaluation

- Completed on `main`: hygiene/runtime separation (#1), runtime lifecycle (#2), chat consolidation (#3), embedding correctness/reference agreement (#5), and the interactive project (#12).
- Core implemented but acceptance validation remains: semantic quality and option-order robustness (#4).
- Partial: CPU performance (#6), verified direct-GGUF delivery through the separate companion (#7), GPU foundations (#8), and release engineering (#9).
- Backlog: verified model delivery (#7), ONNX evaluation (#10), agent/NPC extensions (#13–#16), and the state-changing conversion/toolchain portions of #18.
- Started: separate `salmon-model` companion foundations for live, non-persisted Hugging Face discovery (#17) and discovery-to-preparation planning (#19); it is not linked or packaged with the Godot addon. Exact-plan consent, bounded verified GGUF installation, structural validation, listing, removal, a restrained embedded browser UI, and portable project-model manifests are implemented; full llama.cpp probing, export staging, hardware-fit guidance, and conversion toolchains remain.
- Blocked: clean Windows Godot import (#11).

“Mechanically implemented” is not equivalent to model-quality or release certification.

## Current progress

- Committed hygiene/runtime/chat plus the initial decision and embedding paths in `2b9eb98`, with native and real-model smoke tests. Issues #1–#3 are complete; semantic quality keeps #4 open, while canonical MiniLM cosine-reference validation completed #5.
- Added CPU configuration, separate generation/batch thread controls, Qwen3 no-thinking support, optional native/AVX2 builds, and example-level embedding index caching. The model-agnostic #6 v1 harness now reports GGUF capabilities, cold/warm p50/p95, prompt throughput, time to first token, and generation throughput from external JSON manifests. Memory/frame contention, parameter sweeps, runtime prefix reuse, true cross-text batching, device discovery, and GPU fallback remain (#6/#8).
- Added deployment/migration documentation, source-built desktop CI preview artifacts, and an interactive three-domain Godot showcase (#12). No built-in downloader or production certification yet (#7/#9).
- Preserved the pre-existing modified tracked DLL and uncompiled embedding prototype outside the commit; ordinary development output is isolated under the build directory, with an explicit opt-in target for staging the Godot demo.
- A fresh recursive checkout successfully completed a portable Release build, CTest, explicit Godot staging, and model-free headless smoke test. See `VALIDATION.md` for tested hardware/models, limitations, and the first-import blocker.
