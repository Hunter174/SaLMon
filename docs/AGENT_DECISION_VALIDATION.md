# Agent decision use-case: Qwen3-4B local validation

Status: **prototype validated, not production-ready** (Windows 10, i7-10750H, 16 GB RAM, Quadro T2000 Max-Q 4 GB). Measurements from 2026-10-12; all experiments used the native CPU build (`SALMON_NATIVE=ON`, `SALMON_CUDA=OFF`, `SALMON_VULKAN=OFF`). Do not extrapolate these results to GPU or other hardware.

## Model provenance

- Repository: `unsloth/Qwen3-4B-GGUF`, pinned commit `22c9fc8a8c7700b76a1789366280a6a5a1ad1120`
- File: `Qwen3-4B-Q4_K_M.gguf` (2,497,281,312 bytes)
- SHA-256: `f6f851777709861056efcdad3af01da38b31223a3ba26e61a4f8bf3a2195813a`
- License listed by source: Apache-2.0; verify license/notices when distributing weights.
- Installed through `salmon-model install-plan` / `install` with exact consent and size/SHA verification. Weights live in user-level SaLMon storage, not this repository. Companion's `runtime_validation: not-run` describes its registry flag, not these independent smoke tests.

## Tests performed

- Native build/CTest passed; `salmon_model_smoke chat <Qwen3-4B-GGUF>` passed (chat, repeat/reset, decision, multi-question score shape and ordering).
- Staged Godot 4.5.1 headless `tests/godot/smoke.gd` passed against Qwen3-4B, including `decide_many`. This reused an existing editor cache; it does **not** resolve fresh editor import issue #11.
- `salmon_semantic_benchmark`: 16/16 correct across eight authored easy fixtures and two option orders; 8/8 stable when options were rotated; four-way cyclic ensemble 8/8. This is **not** a statistically meaningful test of NPC motives, long-horizon behavior, or probability calibration. CPU mean was 5,824 ms/decision on the benchmark's longer 4-option prompts; 23,265 ms/four-position ensemble.
- `salmon_performance_benchmark`: `build/qwen4-benchmark.json` / `build/qwen4-performance.json` (ignored local artifacts). CPU, context 1024, batch 256, threads 4, batch threads 8, mmap enabled; one cold run, one warmup, eight warm samples. The chat workload generated eight tokens. **Eight samples do not support a robust p95 estimate.**

| Model / operation | Warm median | Warm mean | Cold load (one run) |
| --- | ---: | ---: | ---: |
| Qwen3-0.6B Q4, decision | 966 ms | 965 ms | 510 ms |
| Qwen3-4B Q4, decision | 4,278 ms | 4,286 ms | 2,108 ms |
| Qwen3-0.6B Q4, chat (8 tokens) | 784 ms | 788 ms | 598 ms |
| Qwen3-4B Q4, chat (8 tokens) | 3,913 ms | 3,899 ms | 2,131 ms |

These workloads are intentionally short and not representative of a fully populated NPC context. Repeated requests are recomputed without a prefix/KV cache. `decide_many` shares a single asynchronous submission/result but currently evaluates each question sequentially; two four-second questions are about eight seconds, **not** one four-second inference. It is unsuitable for per-frame or frequent per-NPC decisions on this CPU.

## Release gates for this use case

1. Resolve #11 (fresh Windows Godot editor import crash); then verify fresh imports, debug/release exports, and a clean-machine installation. Current validation is insufficient for a production release.
2. On this hardware, measure a supported GPU backend (CUDA/Vulkan development toolchain not currently installed) with VRAM/KV-cache headroom **while the game renders**. Do not promise GPU acceleration based on the GPU's nominal 4 GB VRAM.
3. Set an explicit interaction latency/throughput budget and benchmark representative agent contexts, several active agents, `decide_many`, scheduling, queue wait (not included in reported `elapsed_ms`), memory use, timeouts, cancellation, and rendering frame times. Consider lower-frequency offscreen ticks or a smaller model as fallback.
4. Build a representative held-out decision suite for NPC relationships, competing goals, adversarial/ambiguous information and option permutations. Measure action quality and order bias, and calibrate option scores on outcome-labelled data before interpreting them as probabilities or applying confidence gates. Eight easy cases are not evidence of calibration.
5. Verify low-memory behavior, corrupted/incompatible weights, offline operation, packaging of model license/notice and hash-verified model delivery. Do not ship the model in a PCK without extraction to a real path.

SaLMon is already a usable **experimental** decision runtime for a turn-based/offscreen simulation with asynchronous results and deterministic gameplay fallbacks. It is **not** validated as production-ready for responsive, high-volume local NPC inference on this laptop.
