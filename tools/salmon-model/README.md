# salmon-model companion

`salmon-model` is an optional, standalone model discovery and preparation companion for SaLMon. It is **not** part of the GDExtension and is not packaged in the Godot Asset Library addon.

Current scaffold commands:

```sh
salmon-model search --query "small dialogue" --format gguf --limit 20
salmon-model inspect --revision main Qwen/Qwen3-0.6B-GGUF
salmon-model plan --revision main Qwen/Qwen3-0.6B-GGUF
```

All output is versioned JSON. Search and inspection call Hugging Face live; the companion does not persist listings or maintain a model catalog. Live metadata is explicitly treated as unverified. Mutable revisions are resolved to the commit SHA returned by Hugging Face.

The initial scaffold does not download, convert, or quantize anything. Those state-changing commands will require explicit consent and will use temporary files, checksums, validation, and atomic installation.

## Development

Go 1.22 or newer is required only to build the companion:

```sh
cd tools/salmon-model
go test ./...
go build ./cmd/salmon-model
```

The SaLMon C++/Godot build does not depend on Go or this directory.
