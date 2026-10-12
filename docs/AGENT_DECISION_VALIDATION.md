# Agent decision use-case: Qwen3-4B local validation

Status: **local prototype integration validated; not yet highly functional under multi-agent load** (Windows 10, i7-10750H, 16 GB RAM, Quadro T2000 Max-Q 4 GB). Measurements from 2026-10-12; all experiments used the native CPU build (`SALMON_NATIVE=ON`, `SALMON_CUDA=OFF`, `SALMON_VULKAN=OFF`). Do not extrapolate these results to GPU or other hardware.

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

## Prototype agent workload (CPU-only, local)

Reproduce with `build/native/salmon_agent_benchmark.exe /absolute/path/to/Qwen3-4B-Q4_K_M.gguf` after building target `salmon_agent_benchmark`; weights are external. The benchmark uses one asynchronous `salmon::Runtime`, **one shared** Qwen3-4B Q4 model, CPU-only settings (1024 context, 256 batch, four generation threads, eight prompt threads), agent-specific states, 12 authored multi-role scenarios with two option orders, six paired counterfactual scenarios with two orders each, an eight-agent simultaneous burst, and a two-question `decide_many`. Windows memory values are the process's working set/private usage, not total system RAM or GPU VRAM. Outputs are reports, not CTest pass thresholds. Two consecutive local runs in October 2026 gave:

| Measurement | First run | Repeat run |
| --- | ---: | ---: |
| 12 authored scenarios × 2 orders | 24/24 rubric, 12/12 order stable | 24/24, 12/12 |
| 6 paired counterfactuals × 2 scenarios × 2 orders | 20/24 rubric, 12/12 order stable, 4/6 pairs flipped | 20/24, 12/12, 4/6 |
| Single authored decision end-to-end median | 4,225 ms | 4,266 ms |
| Eight simultaneous agents: first / last completed | 4,403 / 33,352 ms | 4,291 / 32,616 ms |
| Eight-agent median service / median queue+poll | 4,165 / 14,894 ms | 4,077 / 14,691 ms |
| Two-question `decide_many` end-to-end | 8,792 ms | 7,934 ms |
| Process working set after model load / peak after workload | not recorded | 3,559 / 4,278 MiB |
| Process private usage after workload | not recorded | 2,055 MiB |

**Observed failures in both runs:** Hesta incorrectly granted a discount despite the authoritative ledger refuting the buyer's claim; Sol incorrectly gave away a ration despite a four-for-four obligation. Both failures persisted in both option orders. The other paired cases (pass validity, medical priority, routes and courier authorization) flipped as authored. All examples are authored and visible to developers, not held-out training/evaluation data; the chosen answers are a local rubric rather than objective ground truth. These results **do not** prove general NPC quality, confidence calibration, safe policy adherence, or fairness. Game-side simulation rules must enforce authoritative ledger/resource constraints and action validity; SaLMon scores alone must not override those rules. Do not change the model prompt and quote these same cases as fresh validation without a separate held-out set.

A separate `--burst-only` run after adding `queue_ms` to runtime completions reported: eight-agent first/last response 4,670/34,124 ms, median backend service 4,240 ms, median **worker queue wait** 15,396 ms, last wait 29,891 ms, and median submit/poll overhead 7 ms. `queue_ms` is measured at worker dequeue; it does not include Godot signal dispatch, and no priority scheduling is implemented. Native CTest and fresh Godot 4.4.1 real-Qwen3-4B smoke tests passed with the new completion field.

A separate **non-headless synthetic rendering probe** (Godot 4.4.1 Windows, 400 animated rectangles, capped at 60 FPS, eight simultaneous decisions) measured wall-clock frame intervals: baseline p95 17.4 ms, while inferring p95 16.9 ms, max 20.4 ms, zero intervals above 33.3 ms across 2,026 rendered frames; last agent result at 33.75 s (queue wait 29.57 s). This shows this simple scene stayed responsive during CPU inference. It is **not** a real game frame-time certification, GPU inference benchmark, or proof that a crowded game scene will stay responsive.

Practical implication: **do not** call this model every frame or submit every NPC in a crowded scene at once. Under a single worker the eighth result arrives after ~33 s on this laptop, even though individual inference is ~4 s; `result.elapsed_ms` excludes queue wait. A prototype can use low-frequency/offscreen turns, request throttling/priority, state-change caches, deterministic fallback actions and independent simulation ticks. Game frame-time under actual rendering, cancellation under load, high-context sessions, and memory pressure remain unmeasured; request budgets must be set for the intended interactions before making a readiness claim.

## Sub-second feasibility on this CPU

A local `threads_batch` sweep of the **same Qwen3-4B Q4** model and a 4-option, ~140-token merchant prompt (one cold load, one warmup, four warm trials per setting) produced warm median decision latencies of **9.50 s** at two batch threads, **6.46 s** at four, **5.46 s** at six and **4.97 s** at eight. This is a small exploratory sweep, not an exhaustive CPU tuning study, but thread tuning alone did not approach 1 s. Current inference clears the model context before every request (`prefill()` calls `reset()`), so repeated or shared prefixes are not reused. Prefix caching/shorter prompts could help some workloads, but **no sub-second Qwen3-4B result has been measured** and different agent states still require work.

For comparison, the existing external Qwen3-0.6B Q4 model on the same 12 authored / six paired-counterfactual benchmark achieved a **916 ms median** single authored decision (some >1 s), 21/24 authored rubric choices, only 15/24 counterfactual choices and 1/6 evidence-sensitive pair flips. The eight-agent burst still took 7.09 s to finish. That smaller general model is faster but **does not meet the tested quality requirement**. These visible examples are not held-out quality certification. If each NPC decision requires a fresh model call under one second, this CPU-only 4B setup is not a viable choice as tested; evaluate a smaller task-adapted model and enforce game-authoritative constraints, or reserve 4B for infrequent asynchronous planning. No GPU inference benchmark is planned for this laptop.

## Local-prototype readiness gates for this use case

1. Use the verified Windows Godot 4.4.1 fresh-import path until #11/upstream Godot #111645 is resolved. **Windows 4.4.1 exported debug/release games now pass local checks**, including Qwen3-4B external GGUF load and `decide` in both builds. Use `tests/godot/export_smoke.py` with separately built debug/release DLLs and matching official export templates, optionally `--model /absolute/path/to/model.gguf`. The test copies the executable/PCK into an isolated installation outside the repository and moves Godot's exported DLL into `addons/salmon/bin/`, matching the descriptor's `res://` path. A previous false failure came from launching in the repository working directory, which loaded a different pre-existing local DLL. The model is not included in the export. Distribution to another machine and other platforms are outside the present local-prototype goal.
2. This laptop's 4 GB VRAM is not a GPU benchmarking target for this use case. Set an explicit **CPU-only** interaction latency/throughput budget, then validate scheduling, backpressure/queue wait, timeouts, cancellation, high-context memory use, and rendering frame times against it. The eight-agent burst measured above is too slow for on-demand crowded-scene behavior. Consider low-frequency offscreen ticks or a smaller model as fallback.
3. Add game-authoritative policy enforcement for ledger obligations and scarce resources (the counterfactual failures above), then test the complete game-policy+SaLMon system against new held-out NPC scenarios, ambiguity/adversarial evidence and more option permutations. Calibrate option scores on outcome-labelled data before interpreting them as probabilities or applying confidence gates.
4. Verify low-memory behavior, corrupted/incompatible weights, and offline operation on this machine. Keep weights in their verified external location; do not ship the model in a PCK without extraction to a real path.

SaLMon is a usable **local prototype** decision runtime for turn-based/offscreen simulation with asynchronous results and deterministic gameplay fallbacks. It is **not** validated for responsive high-volume NPC inference on this laptop; the paired failures require game-policy guardrails before calling it highly functional for this use case.
