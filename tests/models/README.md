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

Initial native-tuned comparison on the same fixtures:

| Quantization | Size | Accuracy | Option-order stability | Mean latency |
| --- | ---: | ---: | ---: | ---: |
| Q4_K_M | ~397 MB | 81.2% | 62.5% | 761 ms |
| Q8_0 | ~639 MB | 87.5% | 75.0% | 809 ms |

Q8_0 SHA-256: `e150ed544dfe6016930c026a93913a5e3184181ebfe6ab2223ae01dd0491784c`.

Q8 improves this tiny fixture set with only a modest CPU latency increase, but neither result is sufficiently order-stable. Treat this model/strategy as experimental until label-position bias is resolved and a larger representative fixture suite passes.

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
