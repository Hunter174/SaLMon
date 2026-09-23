# Migration to the three-capability runtime

## Primary API

`Salmon` is now a **Node**, not a static embedding utility Object. Add it to the scene tree; use `load_model`, `chat`, `decide`, and `embed` with explicit per-node handles. All inference and loading are asynchronous.

The earlier uncommitted `Salmon.get_embeddings(text)` prototype is replaced by:

```gdscript
var model = ai.load_model("user://models/embedding.gguf", "embedding")
# Wait for successful load via request_completed, then:
var request = ai.embed(model, PackedStringArray(["Some text"]))
# Receive result.embeddings[0] via request_completed.
```

The old `src/utility/embedding/` prototype is deliberately retained, unmodified, to preserve existing local work, but **is not compiled**. It loaded a hard-coded model on every call and could call global backend cleanup while other users were active. Do not call or re-add it to the build; remove/archive it after reviewing that local work.

## LLMNode compatibility

`LLMNode` now derives from `Salmon`. Existing `initialize`, `chat_async`, `stream_chat_async`, and legacy output signals remain.

Changes:

- `initialize` queues a load rather than blocking. FIFO ordering permits a chat queued immediately afterward; a failed load causes subsequent chat to fail too.
- Listen to inherited `request_failed` for errors; errors are not returned as chat text.
- Streaming `stream_complete` ends the compatibility stream, including failure/cancellation. Consult `request_failed` to distinguish failure.
- `download_model` returns `ERR_UNAVAILABLE`; it no longer shells out to curl with caller-provided input.
- Reinitialization queues unload/load safely. New projects should use explicit handles and handle queue rejection rather than this compatibility convenience API.
- Chat sampling is greedy rather than a hard-coded temperature of 0.8.
- Cancellation, limits and model lifecycle are inherited from `Salmon`.

The old standalone download scripts are legacy developer utilities, not part of the generated runtime artifact or a verified model manager.

## Build hygiene

- Build godot-cpp and llama.cpp from pinned source, not a machine-specific prebuilt archive. The llama.cpp gitlink is updated to `bf78f5439ee8e82e367674043303ebf8e92b4805`, matching the revision already checked out locally; the obsolete duplicate godot-cpp submodule entry is removed.
- Older MinGW SDKs lack the pinned backend's optional thread-power API declarations. CMake disables those newer thread APIs for `ggml-cpu.c` only; it does not patch submodule files or disable mmap. The prior local submodule modifications remain untouched.
- Always configure outside the source tree.
- New libraries appear under the build directory, not in tracked `addons/salmon/bin/`.
- The old checked-in DLL is preserved and does not match the new API.
- Global compile definitions attempting to disable mmap have been removed. `settings.mmap` now controls the actual native load parameter.
- GDExtension hot reload is disabled until lifecycle/reload behavior is explicitly tested. Restart the editor after replacing binaries.
- The release workflow now builds/test artifacts instead of publishing whatever binaries happen to be in the checkout. Publishing remains gated on release validation.
