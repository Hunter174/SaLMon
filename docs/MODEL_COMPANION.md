# Model companion architecture

Issues #17–#19 are implemented outside the Godot runtime through an optional `salmon-model` companion. The companion is distributed separately from the Godot Asset Library addon.

## Boundary

The GDExtension remains offline inference code. It loads validated filesystem GGUF files and has no HTTP, Hugging Face, credential, downloader, Python, conversion, or quantization dependency.

The companion owns live discovery, compatibility planning, explicit installation, installed-model provenance, and optional preparation toolchains. Exported games do not require it.

## Live discovery, not a catalog

The companion does not ship or persist a model catalog. Every `search`, `inspect`, or `plan` invocation queries Hugging Face's live Hub API. Responses exist only in process memory and command output.

- `GET /api/models` performs search.
- `GET /api/models/{owner}/{repo}/revision/{revision}?blobs=true` resolves repository details, commit identity, files, sizes, and LFS/Xet hashes.
- A branch or tag is not an installation identity. The resolved commit SHA and selected file content SHA-256 are recorded when a user later chooses to install.

Live metadata is untrusted input. Search presence, tags, popularity, and a `.gguf` suffix do not certify runtime compatibility. The companion reports `direct_gguf_candidate`, `unverified_conversion_candidate`, or `unknown_or_unsupported`; only local validation can promote an installed model to validated status.

No hosted inference endpoint is used.

## Recommended sources and starter models

The UI contains a small, auditable list of recommended Hugging Face source identities—not a mirrored model catalog. Selecting a source performs a fresh Hub query scoped to that account. Initial sources are upstream publishers Qwen and Nomic AI; the llama.cpp/GGUF organization ggml-org; established quantization publishers Unsloth and bartowski; LM Studio Community; and the Second State embedding ecosystem. Hugging Face itself identifies ggml-org, Unsloth, LM Studio Community, and bartowski as prominent GGUF publishers in its [GGUF/llama.cpp integration overview](https://huggingface.co/blog/transformers-llama-cpp-quants).

Each source entry records its category, rationale, caveat, purposes, URL, and review date. Inclusion means the identity and publishing practice were reviewed; it does not endorse every repository owned by that account. Source-model quality, inherited terms, experimental formats, runtime support, and suitability still vary.

A separate starter set is deliberately limited to exact artifacts SaLMon has exercised:

- `unsloth/Qwen3-0.6B-GGUF` Q4_K_M and Q8_0 for chat/decision mechanics. Semantic quality remains uncertified.
- `second-state/All-MiniLM-L6-v2-Embedding-GGUF` Q4_K_M for embeddings, validated against canonical FP32 cosine geometry.

Starter records are pinned by repository commit, filename, size, SHA-256, validation status, and review date. They are guidance, not a claim of safety or legal certification.

## Discovery-to-preparation assessment

For repositories without GGUF files, `inspect` and the browser UI now compare declared `config.architectures` values with a generated matrix from the pinned llama.cpp `convert_hf_to_gguf.py` at commit `bf78f5439ee8e82e367674043303ebf8e92b4805`. The assessment reports:

- direct GGUF availability, recognized conversion candidates, incomplete sources, unknown architectures, or unsupported architectures;
- detected configuration, tokenizer, weight, and weight-index files;
- missing source requirements and absent source-weight SHA-256 metadata;
- source-weight totals plus explicitly approximate F16, Q8, Q5, and Q4 output/peak-disk ranges;
- multimodal-projector caveats because SaLMon's current API supports text and embeddings, not multimodal inputs;
- distinct actions for finding a GGUF, using a local GGUF, and preparing source weights.

The conversion action intentionally remains disabled until the separate pinned, consented toolchain from issue #18 is available. Recognition by the converter matrix is not runtime compatibility or output-quality certification. PyTorch pickle-only sources receive an additional isolation warning; arbitrary repository code and `trust_remote_code` remain prohibited.

Maintainers regenerate the committed matrix after updating pinned llama.cpp with:

```sh
python tools/salmon-model/scripts/generate_converter_matrix.py
```

Python is not needed to build or run `salmon-model`; this script is only a source-generation check. Tests compare the generated matrix with the pinned converter when the submodule is present.

## Target hardware and filtering

The UI accepts a session-level deployment target: system RAM, optional VRAM, maximum model download size, and CPU-first or GPU-assisted mode. This is intentionally editable because the developer's workstation may not represent players' machines. The browser's coarse memory report is only an initial hint and is never silently treated as a shipping requirement.

Fit labels use a conservative planning estimate of `file size × 1.25 + 0.5 GiB`, reserve 2 GiB of system RAM for the OS, and in GPU-assisted mode count 90% of declared VRAM alongside RAM. Labels mean only that weights and rough overhead may fit: context length, KV cache, architecture, batching, backend allocations, and other game memory can materially increase usage. They are not performance or compatibility certification.

Unrestricted Hub results can be filtered in memory by candidate purpose, quantization, declared/missing license metadata, and maximum GGUF file size. The target profile and filters do not create or persist a model-listing cache.

## Persistence

Discovery listings—including live inventories from recommended sources—are never cached by SaLMon. Persistence is limited to user-created state:

- Models explicitly installed by the user.
- Models explicitly converted or quantized by the user.
- Minimal provenance and validation records required to list, select, verify, and remove those files.
- Optional pinned preparation toolchains installed with explicit consent.

The first preparation toolchain is stored under `toolchains/llama-quantize/b6002/<os>-<arch>/`, with its separate registry under `toolchains/registry/`. Temporary archives use the existing `downloads/` directory and are removed on success, cancellation, or failure.

## User interfaces

`salmon-model ui` starts the proof-of-concept graphical workflow in the user's browser. HTML, CSS, and JavaScript are embedded in the same executable; no Node.js/Electron runtime is required. The on-demand server binds to IPv4 loopback on a random port, uses a per-launch mutation token plus strict origin checks, applies a restrictive Content Security Policy, and can be stopped from the page or with `Ctrl+C`. It calls the same Go packages as the CLI rather than spawning CLI subprocesses.

The POC supports live search, repository/file inspection, exact installation-plan review, explicit consent, progress, cancellation, installed-model management, and project-manifest assignment. Its restrained desktop-tool layout follows patterns from [Hugging Face's scannable model list](https://huggingface.co/models), [LM Studio's repository-first quantization selection](https://lmstudio.ai/docs/app/basics/download-model), [Jan's plain-language local-model guidance](https://github.com/janhq/jan/blob/dev/docs/src/pages/docs/desktop/manage-models.mdx), and [Docker Desktop's separation of Hub and local images](https://docs.docker.com/desktop/use-desktop/images/). It does not persist discovery results or run as a permanent service.

## Protocol

Commands emit a versioned JSON document. Long-running state-changing commands emit JSON Lines progress events. This protocol remains available for terminals, automation, and a future Godot editor frontend without linking the companion into the extension.

Discovery commands are read-only:

```text
salmon-model search --query TEXT [--format any|gguf] [--limit N]
salmon-model inspect [--revision REV] OWNER/REPOSITORY
salmon-model plan [--revision REV] OWNER/REPOSITORY
```

Direct installation uses two-phase consent:

```text
salmon-model install-plan --file FILE [--revision REV] OWNER/REPOSITORY
salmon-model install --file FILE --consent DIGEST [--revision REV] OWNER/REPOSITORY
salmon-model list
salmon-model remove --id INSTALLATION_ID
```

The digest binds the resolved commit, exact repository file, reported size, content SHA-256, license, and destination. Installation refuses files without a Hub-reported SHA-256, applies a configurable size limit, streams to a same-store temporary file, verifies size/hash and the GGUF header, then atomically promotes it. Structural validation is not represented as successful llama.cpp runtime validation.

Optional quantizer-toolchain acquisition has its own consent boundary:

```text
salmon-model toolchain-plan [--root PATH]
salmon-model toolchain-install --consent DIGEST [--root PATH]
salmon-model toolchain-list [--root PATH]
salmon-model toolchain-remove --id TOOLCHAIN_ID [--root PATH]
```

The catalog pins official llama.cpp `b6002` archives by platform, exact byte size, SHA-256, and GitHub release URL. Installation accepts only catalog-matching plans, HTTPS downloads and redirects, bounded ZIPs without traversal/symlink/special entries, and a successful `llama-quantize` help probe. Extraction is staged and atomically promoted. Installing the executable does not authorize running it; quantization will use a separate exact execution plan and consent digest.

## Project manifest

The UI can assign an explicitly installed model to a project-level `salmon.models.json`. The manifest records purposes (`chat`, `decision`, or `embedding`), installation ID, SHA-256, repository, immutable revision, source filename, export-relative destination, declared license/link, base-model metadata, and source URL. It deliberately omits the machine-specific installed path.

Creating a manifest does not export or copy weights. A later build-staging command will resolve each installation ID and hash against the local registry, copy the verified GGUF into the game distribution, and verify the staged copy. License information is provenance for developer review, not legal certification.

## Security decisions

- Production Hugging Face access requires HTTPS.
- Repository identifiers and revisions are validated before URL construction.
- Metadata response size and request duration are bounded.
- No repository code is executed.
- No `trust_remote_code` behavior is permitted.
- Download and preparation commands require explicit consent.
- Model and toolchain installation use temporary files, mandatory SHA-256 verification, bounded validation, and atomic promotion.
- Toolchain ZIP extraction rejects path traversal, backslash paths, absolute paths, symlinks, special files, excessive entry counts, and excessive expanded size.
- The browser POC listens only on loopback, uses a random port and per-launch authorization token, checks mutation origins, and exposes no arbitrary URL-fetch endpoint.
- Missing hashes and licenses are shown as warnings, never silently treated as trusted.

## Preparation handoff

A direct GGUF candidate proceeds to the verified installation workflow from #7. A non-GGUF source remains preparation-only until its architecture and required files match the pinned llama.cpp converter's support matrix. Conversion and quantization are delegated to opt-in toolchains managed by the companion, never the GDExtension.

The small `llama-quantize` acquisition path is now implemented independently of the future Python converter environment. It does not yet execute quantization. The next step is an exact `quantize-plan` / `quantize` contract that binds input hash, output destination, preset, executable identity, disk estimate, and cancellation behavior before process launch.
