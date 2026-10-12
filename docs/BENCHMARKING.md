# Local GGUF benchmarking

`salmon_performance_benchmark` is the model-optional v1 CPU benchmark harness. It reads an external JSON manifest, runs only the requested operations, and writes machine-readable JSON. It never downloads a model or contacts Hugging Face.

## Build and run

```sh
cmake -S . -B build/native -DCMAKE_BUILD_TYPE=Release -DSALMON_NATIVE=ON -DBUILD_TESTING=ON
cmake --build build/native --target salmon_performance_benchmark --parallel

# benchmark.example.json uses this variable for its Qwen entry
export SALMON_MODEL_DIR="$PWD/tests/models"
build/native/salmon_performance_benchmark \
  tests/models/benchmark.example.json build/results.json
```

PowerShell:

```powershell
$env:SALMON_MODEL_DIR = "$PWD/tests/models"
build/native/salmon_performance_benchmark.exe `
  tests/models/benchmark.example.json build/results.json
```

Paths are expanded from `${NAME}` variables and then resolved relative to the manifest. Model weights remain external and ignored by Git.

## Manifest

See [`tests/models/benchmark.example.json`](../tests/models/benchmark.example.json). Each model declares:

- A stable ID and local GGUF path.
- Optional source URL, source revision, and SHA-256 identity.
- Requested `chat`, `decision`, and/or `embedding` operations.
- Context, batch, thread, GPU-layer, and mmap settings.

Workloads are independent of model entries, making it possible to compare architectures and quantizations with the same inputs. Model-specific instructions, such as Qwen's `/no_think`, belong in the manifest rather than in the benchmark executable.

## Results

The report includes:

- Host platform and hardware-thread count.
- Declared source identity and actual file size.
- GGUF name, architecture, description, parameter count, training context, embedding dimensions, pooling, and chat-template availability reported by llama.cpp.
- Cold load and first-inference timings.
- Warm min, mean, p50, p90, p95, and maximum latency.
- Prompt tokens, prompt evaluation time, and prompt tokens/second.
- Chat time to first token, generated token count, and generated tokens/second.
- Raw warm samples so results can be reanalyzed without parsing console logs.

An incompatible requested operation is recorded as `"status": "skipped"` with the backend reason. One unsupported model does not prevent other manifest entries from running.

## Interpretation

- Run a fresh benchmark process when measuring cold startup. `cold_runs` reloads the model but does not flush the operating system's filesystem cache.
- Warm statistics exclude configured warmup iterations.
- Use the exact same manifest, model hashes, build profile, and machine power settings for comparisons.
- Native builds describe the current machine and are not a portable deployment baseline.
- Small run counts are useful for smoke testing, not performance claims. Use at least 30–50 warm runs for p95 reporting.
- Semantic timing does not imply semantic correctness. Run `salmon_semantic_benchmark` separately.

## Authored NPC workload (model-optional)

Build `salmon_agent_benchmark` and run it against an **external** chat-capable GGUF with an absolute path. It does not download weights or run automatically in CTest:

```text
cmake --build build/native --target salmon_agent_benchmark --parallel
build/native/salmon_agent_benchmark.exe C:/path/to/Qwen3-4B-Q4_K_M.gguf
build/native/salmon_agent_benchmark.exe C:/path/to/Qwen3-4B-Q4_K_M.gguf --burst-only
```

This CPU-only report checks 12 multi-role authored cases in two option orders, six paired authoritative-fact counterfactuals in two orders, one burst of eight agents sharing a single worker/model, and a two-question `decide_many`. It reports end-to-end response latency, native worker `queue_ms`, backend `elapsed_ms` and submit/poll overhead separately; on Windows it prints process working set/private usage. These are **visible authored rubrics, not held-out quality tests**. See [agent decision validation](AGENT_DECISION_VALIDATION.md) for local results and limits. Neither favorable scores nor 8 burst samples establish p95, calibration, rendered frame times or broad NPC reliability.

## V1 boundary

V1 establishes extensible manifests, capability reporting, cold/warm percentiles, and operation throughput. Process RSS/peak memory, automated parameter sweeps, cancellation latency, Godot frame-time contention, prefix reuse, and true cross-text embedding batching remain subsequent #6 work. The result schema is versioned so those measurements can be added without hardcoding a model family.
