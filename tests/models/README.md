# Sample models for the three-domain test project

Model weights are intentionally ignored by Git and are not redistributed by SaLMoN. Download them into this directory before running the interactive project.

## Chat + semantic decisions

- Model: Qwen/Qwen3-0.6B
- GGUF source: [unsloth/Qwen3-0.6B-GGUF](https://huggingface.co/unsloth/Qwen3-0.6B-GGUF)
- File: `Qwen3-0.6B-Q4_K_M.gguf`
- Approximate size: 397 MB
- SHA-256: `ac2d97712095a558e31573f62f466a3f9d93990898b0ec79d7c974c1780d524a`
- License: Qwen model license/Apache 2.0 as stated by the model card; verify current upstream terms.

Download with Hugging Face CLI:

```sh
huggingface-cli download unsloth/Qwen3-0.6B-GGUF \
  Qwen3-0.6B-Q4_K_M.gguf --local-dir tests/models
```

Qwen3 is used for both chat and experimental semantic decisions. Semantic scoring is direct next-token logit scoring, not a separate classifier. The sample project validates the mechanism and displays scores; it does not certify decision accuracy or calibration.

After building tests, run the model-optional fixture report with:

```sh
build/native/salmon_semantic_benchmark tests/models/Qwen3-0.6B-Q4_K_M.gguf
```

The initial Q4_K_M result was 81.2% across base/rotated trials but only 62.5% option-order stability. Treat this model/strategy as experimental until label-position bias is resolved and representative game fixtures pass.

## Embeddings

The repository currently contains the small sample embedding model at:

```text
addons/salmon/models/all-MiniLM-L6-v2-Q4_K_M.gguf
```

- SHA-256: `2ec4cee28a27a9c973d5f5230930d6ef6e52694bd2bc71be26a9bef5b1d755e6`
- Approximate size: 20 MB

## Run

Build the extension, copy the generated `addons/` directory into `tests/godot/`, and run `tests/godot/main.tscn`. The project defaults to these paths, or accepts absolute overrides:

```sh
SALMON_CHAT_MODEL=/absolute/path/Qwen3-0.6B-Q4_K_M.gguf \
SALMON_EMBEDDING_MODEL=/absolute/path/all-MiniLM-L6-v2-Q4_K_M.gguf \
Godot --path tests/godot
```

The smoke script also uses these environment variables. Do not commit the GGUF files.
