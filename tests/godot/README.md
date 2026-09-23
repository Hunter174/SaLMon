# Three-domain Godot test project

This project provides three concise, interactive showcases plus a headless smoke test:

- `chat.tscn` — **Campfire Companion:** streamed, multi-turn Qwen3 dialogue with an authored persona and bounded context.
- `semantic.tscn` — **Living Blacksmith:** arbitrary player speech is scored against safe authored reactions; deterministic code applies consequences and uses a low-margin fallback.
- `embeddings.tscn` — **Lore Lens:** MiniLM ranks untagged journal records against a natural-language question by meaning rather than exact keywords.

Together they demonstrate three different jobs: generate presentation, constrain a decision, and retrieve existing content.

The model weights are not committed. See `../models/README.md`.

## Setup

1. Build SaLMoN from the repository root, then run `cmake --build build/refactor --target salmon_stage_godot_test` (replace `build/refactor` with your build directory). This explicit target stages the extension into `tests/godot/addons/salmon/`; ordinary builds stay out-of-tree.
2. Confirm `tests/godot/addons/salmon/salmon.gdextension` and a platform-matching binary exist. If they do not, Godot cannot recognize `Salmon` and the saved scene will show an unknown node type.
3. Download the Qwen3 model into `tests/models/`, or set absolute model environment variables.
4. If the project was opened before the extension was staged, close Godot and delete `tests/godot/.godot/` to clear stale class/extension caches, then reopen this directory in Godot.
5. Run `main.tscn` for the tabbed gallery, or use **Run Current Scene** on `chat.tscn`, `semantic.tscn`, or `embeddings.tscn`. Each showcase creates and loads its own `Salmon` runtime when launched independently.

The native extension must be built for the editor/export target. Restart the editor after replacing the DLL; hot reload is intentionally disabled for the current preview.

For responsive local testing, prefer a clean `-DSALMON_NATIVE=ON` build on your own machine. The portable build deliberately avoids newer x86 instructions and can be dramatically slower. The demos default to four generation threads and up to eight batch threads; override them with `SALMON_THREADS` and `SALMON_THREADS_BATCH`. Lore Lens caches document vectors after its first search, so later searches embed only the query. The current Windows/Godot 4.5.1 setup also has a fresh headless editor-import crash tracked in issue #11; if import crashes before the cache is created, use the documented headless smoke workaround or a supported Godot/binding version while #11 is investigated.

## Automated check

```sh
Godot --headless --path tests/godot --script smoke.gd
```

With real models:

```sh
SALMON_CHAT_MODEL=/absolute/path/Qwen3-0.6B-Q4_K_M.gguf \
SALMON_EMBEDDING_MODEL=/absolute/path/all-MiniLM-L6-v2-Q4_K_M.gguf \
Godot --headless --path tests/godot --script smoke.gd
```

The script checks runtime behavior and score/vector shape. It is not a model-quality benchmark. The interactive semantic page explicitly labels its scores as conditional and uncalibrated.
