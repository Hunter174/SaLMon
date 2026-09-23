# SaLMoN — local AI for Godot

SaLMoN is a Godot 4.4+ GDExtension for **chat generation, semantic decisions, and embeddings**, using local GGUF models through llama.cpp. No Python, cloud inference, API key, or network connection is required at runtime.

**Development preview:** the runtime/API refactor is implemented, not a production release. Build from source; the historical DLL tracked in `addons/salmon/bin/` does **not** implement the new API. See [validation status](docs/VALIDATION.md), including the Windows first-import blocker, before shipping.

## Architecture

- One native extension per platform; model weights are separate files.
- `Salmon` is a Node with explicit model handles.
- A chat model can serve both chat and decisions. Use a dedicated embedding model for useful sentence vectors.
- One lazily started, owned worker per node serializes model operations. Signals are emitted on Godot's main thread in `_process`.
- Load/unload/inference are asynchronous; up to 32 outstanding requests and 8 loaded models per node.
- No detached threads, shell downloads, or automatic network access.

## Build

Requires CMake 3.21+, Python 3 for generating Godot C++ bindings, a C++17 compiler, and the pinned submodules. Python is a **build dependency only**.

```sh
git submodule update --init --recursive
cmake -S . -B build/dev -DCMAKE_BUILD_TYPE=Release -DGODOTCPP_TARGET=template_debug
cmake --build build/dev --config Release --parallel 4
ctest --test-dir build/dev -C Release --output-on-failure
```

For MinGW add `-G "MinGW Makefiles"` on first configure. For exported release games use a separate build directory with `-DGODOTCPP_TARGET=template_release`. Compiler optimization (`CMAKE_BUILD_TYPE`) and Godot target (`GODOTCPP_TARGET`) are separate settings.

Copy **`build/dev/addon/addons/salmon/`** into your project's `addons/` directory. For the repository test project, explicitly stage the fresh extension with `cmake --build build/dev --target salmon_stage_godot_test`; ordinary builds remain out-of-tree. Build output never overwrites the historical checked-in binary. This is a GDExtension, not an editor plugin: there is no Plugins checkbox to enable.

If Godot reports `Salmon` as an unknown node, the extension is not loaded: confirm the platform-matching binary exists under `addons/salmon/bin/`, close Godot, delete the project's `.godot/` cache, and reopen it. A fresh Windows/Godot 4.5.1 import crash can prevent that cache from being created; this is tracked in issue #11 and is separate from the runtime registration (which passes headless tests).

CPU execution is the default. Redistributable x86 builds do not require AVX2 by default. Optional build settings:

| Setting | Purpose |
| --- | --- |
| `SALMON_CPU_AVX2=ON` | Faster x86 build requiring AVX2/FMA/F16C/BMI2/SSE4.2 |
| `SALMON_NATIVE=ON` | Tune to the build machine; not a portable distribution |
| `SALMON_VULKAN=ON` | Compile Vulkan support; requires Vulkan SDK |
| `SALMON_CUDA=ON` | Compile CUDA support; requires CUDA toolkit |
| `SALMON_METAL=ON` | Compile Metal support on Apple platforms |
| `SALMON_BUILD_EXTENSION=OFF` | Build native runtime/tests without Godot bindings |

For local development on a known machine, a clean native build is strongly recommended:

```sh
cmake -S . -B build/native -DCMAKE_BUILD_TYPE=Release -DSALMON_NATIVE=ON
cmake --build build/native --parallel
```

Do not redistribute that binary to CPUs with different instruction support. Use `SALMON_CPU_AVX2=ON` for an explicitly AVX2-targeted package, or the portable defaults for maximum compatibility. On an AVX2-capable test machine, native tuning reduced the Qwen3-0.6B semantic demo from roughly 16 seconds to under 2 seconds.

GPU switches are build foundations, **not validated GPU packages or automatic device selection/fallback**. If offload is unavailable, request CPU explicitly with `gpu_layers=0`. Account for rendering contention and driver/runtime dependencies. See [roadmap](docs/ROADMAP.md).

## Usage

```gdscript
extends Node

var ai: Salmon
var chat_model: int
var embedding_model: int

func _ready() -> void:
    ai = Salmon.new()
    add_child(ai)
    ai.request_completed.connect(_on_completed)
    ai.request_failed.connect(_on_failed)
    ai.token_received.connect(_on_token)

    # In an exported game these must be actual filesystem files, not PCK-only assets.
    chat_model = ai.load_model("user://models/chat.gguf", "chat", {
        "context": 2048, "threads": 4, "threads_batch": 8,
        "batch": 256, "gpu_layers": 0, "mmap": true
    })
    embedding_model = ai.load_model("user://models/embedding.gguf", "embedding", {
        "context": 512, "threads": 4, "threads_batch": 8
    })

func _on_completed(request_id: int, result: Dictionary) -> void:
    if result.operation == "load" and result.model == chat_model:
        ai.chat(chat_model, [
            {"role": "user", "content": "Welcome the traveler in one sentence."}
        ], 64, true)
        ai.decide(chat_model, "The player is injured.", "Which action restores health?", [
            {"id": "heal", "description": "Drink a healing potion"},
            {"id": "attack", "description": "Attack the enemy"}
        ])
    elif result.operation == "load" and result.model == embedding_model:
        ai.embed(embedding_model, PackedStringArray(["A quiet village", "A dangerous cave"]))
    elif result.operation == "chat":
        print(result.text)
    elif result.operation == "decide":
        print(result.choice_id, result.scores)
    elif result.operation == "embed":
        print(result.embeddings)

func _on_failed(request_id: int, error: String) -> void:
    push_warning("Request %s: %s" % [request_id, error])

func _on_token(request_id: int, text: String) -> void:
    print(text) # UTF-8-complete chunks, not necessarily one tokenizer token
```

### API contract

All methods are called from the **main thread**. Submission methods return a positive request ID, or `0` on synchronous rejection (logged through `push_error`; no completion signal for ID 0).

| Method | Behavior |
| --- | --- |
| `load_model(path, purpose, settings={})` | Purpose `chat` or `embedding`; returned request ID is also the reserved model handle |
| `unload_model(model)` | Frees the handle after earlier queued work |
| `chat(model, messages, max_tokens=128, stream=false)` | Greedy text generation; roles: system/user/assistant |
| `decide(model, state, question, options)` | Experimental choice among 2–26 unique IDs with descriptions |
| `embed(model, texts)` | 1–128 texts; normalized pooled vectors in input order |
| `cancel(request_id)` | Best-effort cooperative cancellation of queued/active work |
| `cancel_all()` | Cancels outstanding work; does not unload already loaded models |

Every accepted request gets one terminal signal while the node is alive and processing:

- `request_completed(request_id, result)`
- `request_failed(request_id, error)`

Streaming additionally emits `token_received(request_id, text)`. Completion dictionaries include `operation`, `model`, `model_path`, `purpose`, and `elapsed_ms` (worker execution time, excluding queue wait). Chat adds `text` and `finish_reason`; decisions add `choice_id`, `option_ids`, `logits`, `scores`, `probability_status`, `prompt_version`; embeddings add `embeddings`.

A failed/cancelled load leaves no usable handle. A completed operation wins a cancellation race; cancellation does not undo an already completed load/unload. Partial streamed text may precede a cancelled chat. Errors are never returned as fake generated text.

The node must be in a processing scene tree to drain results. During pause, signals wait unless the developer sets `process_mode` appropriately. Use `queue_free()`, not immediate `free()` from a signal callback. Destruction cancels and joins the worker; it can wait for a native operation that does not support immediate abort. GPU cancellation is checked between evaluations; the pinned backend's in-evaluation abort is CPU-specific.

### Limits and semantics

- Request text budget: 4 MiB; no NUL bytes. Chat: at most 256 messages, 1–32768 output tokens.
- Context: 32–131072 tokens; `threads` and `threads_batch`: 1–256; batch: 1–context. `threads` controls single-token generation while `threads_batch` controls prompt and embedding evaluation. Practical limits are usually much smaller and hardware dependent.
- `gpu_layers=0` means CPU; positive values request partial offload; `-1` requests full offload. Availability is not a guarantee of sufficient VRAM.
- Every chat/decision currently starts from fresh state. Provide the complete conversation. Prefix caching is a follow-up, not implemented.
- Chat uses deterministic greedy sampling in this first version; it does not preserve the old fixed temperature of 0.8.
- Decisions score validated single-token **letter labels** at the formatted prompt boundary, without sampling output tokens. Option descriptions are input, not output labels. Unsupported token boundaries fail explicitly.
- Decision scores sum to one **over the supplied choices**. They are not calibrated confidence, correctness guarantees, or a security boundary. This implements the SemIf-style pattern, not its exact prompts or benchmark claims.
- Embeddings require supported mean/CLS/last sequence pooling. Multiple texts reuse a loaded model but are evaluated sequentially; cross-text batching is not implemented. In this pinned llama.cpp, BERT's evaluation path is `llama_decode`, which internally dispatches to its encoder.
- Model/template support depends on the pinned llama.cpp revision. A GGUF extension alone does not guarantee compatibility. No claim is made that newer Qwen/MiniCPM architectures run on this pin.

## Deployment and migration

See [deployment](docs/DEPLOYMENT.md), [migration](docs/MIGRATION.md), [local GGUF benchmarking](docs/BENCHMARKING.md), and the [implementation backlog](docs/ROADMAP.md).

Bundled, pre-downloaded, and user-supplied local files work. A verified opt-in model download manager is planned, not implemented. Never embed multi-GB weights in the native library.

## Tests

Native unit tests do not require model weights. Optional real-model checks:

```sh
build/dev/salmon_model_smoke chat /absolute/path/chat.gguf
build/dev/salmon_model_smoke embedding /absolute/path/embedding.gguf
```

On Windows use `.exe`; with multi-config generators the executables may be under `Release/`. Godot test project/script: `tests/godot/`. See [validation](docs/VALIDATION.md) for the reproducible commands and known first-import failure.

## Licenses

SaLMoN, llama.cpp, Godot bindings, and each model have their own applicable license files. Include dependency notices in distributions and verify each model's redistribution/commercial-use terms. SaLMoN's license does not grant rights to arbitrary model weights. Runtime build artifacts contain no model weights.
