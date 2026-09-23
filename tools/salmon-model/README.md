# salmon-model companion

`salmon-model` is an optional, standalone model discovery and preparation companion for SaLMon. It is **not** part of the GDExtension and is not packaged in the Godot Asset Library addon.

Launch the proof-of-concept graphical workflow:

```sh
salmon-model ui
```

This opens a restrained desktop-style browser interface with an auditable starter set, live inventories from reviewed Hugging Face source identities, unrestricted and filterable Hub search, editable target-hardware fit estimates, repository and quantization inspection, license/provenance review, verified download progress/cancellation, local-model management, and assignment to a project-level `salmon.models.json`. Recommended-source inventories are queried live and are not stored; recommendation never substitutes for compatibility, quality, safety, or license review. The temporary server binds only to `127.0.0.1` on a random port and stops through the UI, `Ctrl+C`, or process termination. Use `salmon-model ui --no-open` to print the URL without opening a browser.

The automation-friendly CLI remains available:

```sh
salmon-model search --query "small dialogue" --format gguf --limit 20
salmon-model inspect --revision main Qwen/Qwen3-0.6B-GGUF
salmon-model plan --revision main Qwen/Qwen3-0.6B-GGUF
salmon-model install-plan --file Qwen3-0.6B-Q8_0.gguf Qwen/Qwen3-0.6B-GGUF
salmon-model install --file Qwen3-0.6B-Q8_0.gguf --consent CONSENT_DIGEST Qwen/Qwen3-0.6B-GGUF
salmon-model list
salmon-model remove --id INSTALLATION_ID
salmon-model toolchain-plan
salmon-model toolchain-install --consent CONSENT_DIGEST
salmon-model toolchain-list
salmon-model toolchain-remove --id llama-quantize-b6002-OS-ARCH
```

CLI output is versioned JSON. Search and inspection call Hugging Face live; the companion does not persist listings or maintain a model catalog. Live metadata is explicitly treated as unverified. Mutable revisions are resolved to the commit SHA returned by Hugging Face.

For non-GGUF repositories, inspection compares declared architectures with a generated matrix from SaLMon's pinned llama.cpp converter. It distinguishes recognized, incomplete, unknown, and unsupported sources; lists detected source files and missing requirements; and reports approximate output and peak-disk ranges. Preparation remains disabled until the separate pinned toolchain is implemented, and converter recognition is not runtime certification.

`install-plan` binds the resolved commit, exact file, reported size, content SHA-256, license, and destination into a consent digest. `install` regenerates the live plan and proceeds only when that exact digest is supplied. It uses a bounded temporary file, verifies size and SHA-256, checks the GGUF header, and atomically promotes the file into user-level managed storage. Download progress is JSON Lines on stderr; the final record is JSON on stdout.

Installation performs structural validation only and records runtime validation as `not-run`.

`toolchain-plan` selects an exact pinned llama.cpp `b6002` release archive for Windows x64, Linux x64, macOS x64, or macOS arm64. The plan binds platform, archive, byte size, SHA-256, upstream URL, and user-level destination into a consent digest. `toolchain-install` downloads only after that consent, enforces HTTPS and size bounds, verifies SHA-256, rejects unsafe ZIP paths, extracts through a staging directory, probes `llama-quantize --help`, and atomically registers the result. Toolchain installation does **not** authorize execution against a model. Quantization plans and execution are the next separate consent boundary; source-model conversion is not implemented yet.

## Development

Go 1.22 or newer is required only to build the companion:

```sh
cd tools/salmon-model
go test ./...
go build ./cmd/salmon-model
```

The SaLMon C++/Godot build does not depend on Go or this directory.
