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

## Persistence

Discovery listings are never cached by SaLMon. Persistence is limited to user-created state:

- Models explicitly installed by the user.
- Models explicitly converted or quantized by the user.
- Minimal provenance and validation records required to list, select, verify, and remove those files.
- Optional pinned conversion toolchains installed with explicit consent.

## Protocol

Commands emit a versioned JSON document. Long-running state-changing commands will emit JSON Lines progress events. This protocol is intended for terminals and a future editor frontend without linking the companion into the extension.

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

## Security decisions

- Production Hugging Face access requires HTTPS.
- Repository identifiers and revisions are validated before URL construction.
- Metadata response size and request duration are bounded.
- No repository code is executed.
- No `trust_remote_code` behavior is permitted.
- Download and preparation commands will require explicit consent.
- Installation will use temporary files, SHA-256 verification where supplied, local GGUF validation, and atomic promotion.
- Missing hashes and licenses are shown as warnings, never silently treated as trusted.

## Preparation handoff

A direct GGUF candidate proceeds to the verified installation workflow from #7. A non-GGUF source can only be called an unverified conversion candidate until its architecture and required files match the pinned llama.cpp converter's support matrix. Conversion and quantization are delegated to opt-in toolchains managed by the companion, never the GDExtension.
