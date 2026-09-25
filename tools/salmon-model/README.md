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
salmon-model conversion-toolchain-plan
salmon-model conversion-toolchain-install --consent CONSENT_DIGEST
salmon-model conversion-toolchain-list
salmon-model conversion-toolchain-remove --id TOOLCHAIN_ID
salmon-model convert-plan --outtype f16 OWNER/REPOSITORY
salmon-model convert --outtype f16 --consent CONSENT_DIGEST OWNER/REPOSITORY
salmon-model quantize-plan --input model-f16.gguf --preset Q4_K_M
salmon-model quantize --input model-f16.gguf --preset Q4_K_M --consent CONSENT_DIGEST
```

CLI output is versioned JSON. Search and inspection call Hugging Face live; the companion does not persist listings or maintain a model catalog. Live metadata is explicitly treated as unverified. Mutable revisions are resolved to the commit SHA returned by Hugging Face.

For non-GGUF repositories, inspection compares declared architectures with a generated matrix from SaLMon's pinned llama.cpp converter. It distinguishes recognized, incomplete, unknown, and unsupported sources; lists detected source files and missing requirements; and reports approximate output and peak-disk ranges. Supported Safetensors sources can now be converted after the isolated toolchain is installed and a separate exact conversion plan is consented. The local browser UI presents distinct Find GGUF, Convert source, and Quantize GGUF actions with separate review dialogs, consent, cancellable jobs, and managed-model handoff. Long identities show compact previews with expandable full values and copy controls; source and license links remain accessible for review. Converter recognition is not runtime certification.

`install-plan` binds the resolved commit, exact file, reported size, content SHA-256, license, and destination into a consent digest. `install` regenerates the live plan and proceeds only when that exact digest is supplied. It uses a bounded temporary file, verifies size and SHA-256, checks the GGUF header, and atomically promotes the file into user-level managed storage. Download progress is JSON Lines on stderr; the final record is JSON on stdout.

Installation performs structural validation only and records runtime validation as `not-run`.

`toolchain-plan` selects an exact pinned llama.cpp `b6002` release archive for Windows x64, Linux x64, macOS x64, or macOS arm64. The plan binds platform, archive, byte size, SHA-256, upstream URL, and user-level destination into a consent digest. `toolchain-install` downloads only after that consent, enforces HTTPS and size bounds, verifies SHA-256, rejects unsafe ZIP paths, extracts through a staging directory, probes `llama-quantize --help`, and atomically registers the result. The larger conversion environment is independently optional. `conversion-toolchain-plan` pins standalone `uv` 0.7.12, Python 3.11.13 from python-build-standalone release 20250712, llama.cpp converter source commit `bf78f5439ee8e82e367674043303ebf8e92b4805`, and a platform-specific SHA-256 dependency lock using CPU-only PyTorch. The consent plan lists all bootstrap artifacts, exact hashes and sizes, 27 locked packages, a dependency working-space allowance, and a conservative installed-size estimate. Installation verifies each bootstrap artifact, safely extracts it, installs only hash-locked binary wheels into a isolated user-level site-packages target, bounds temporary and installed storage, probes imports plus converter help, and atomically registers the isolated environment. It never modifies system Python and does not authorize source downloads or conversion execution.

Quantizer installation does **not** authorize execution against a model. `quantize-plan` separately binds the input path, size and SHA-256; output name and managed destination pattern; preset; size/disk estimates; and archive plus executable identities. `quantize` regenerates that plan, requires its exact consent digest, refuses implicit requantization, emits throttled progress, supports cancellation, bounds and structurally validates the generated GGUF, verifies that the input did not change during execution, and atomically registers the content-addressed output in the normal model registry. Runtime inference validation remains `not-run`. `convert-plan` / `convert` separately bind immutable revision, content-verified root-level Safetensors/config/tokenizer files, F16/BF16 output, source/output/working limits, destination, and hashed isolated environment. The pinned converter copy replaces all seven upstream `trust_remote_code=True` call sites with `False` before use. Neither conversion-toolchain installation nor conversion consent authorizes quantization.

## Development

Go 1.22 or newer is required only to build the companion:

```sh
cd tools/salmon-model
go test ./...
go build ./cmd/salmon-model
```

The SaLMon C++/Godot build does not depend on Go or this directory.
