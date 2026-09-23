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
```

CLI output is versioned JSON. Search and inspection call Hugging Face live; the companion does not persist listings or maintain a model catalog. Live metadata is explicitly treated as unverified. Mutable revisions are resolved to the commit SHA returned by Hugging Face.

`install-plan` binds the resolved commit, exact file, reported size, content SHA-256, license, and destination into a consent digest. `install` regenerates the live plan and proceeds only when that exact digest is supplied. It uses a bounded temporary file, verifies size and SHA-256, checks the GGUF header, and atomically promotes the file into user-level managed storage. Download progress is JSON Lines on stderr; the final record is JSON on stdout.

Installation performs structural validation only and records runtime validation as `not-run`. Conversion and quantization are not implemented yet.

## Development

Go 1.22 or newer is required only to build the companion:

```sh
cd tools/salmon-model
go test ./...
go build ./cmd/salmon-model
```

The SaLMon C++/Godot build does not depend on Go or this directory.
