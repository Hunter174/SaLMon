extends Control

@onready var ai: Node = $Salmon
@onready var status: Label = $Margin/Column/Status
@onready var chat_path_label: Label = $Margin/Column/Models/ChatPath
@onready var embedding_path_label: Label = $Margin/Column/Models/EmbeddingPath
@onready var chat_demo: Node = $Margin/Column/Tabs/Chat
@onready var semantic_demo: Node = $Margin/Column/Tabs/Semantic
@onready var embedding_demo: Node = $Margin/Column/Tabs/Embeddings
var chat_model := 0
var embedding_model := 0
var chat_ready := false
var embedding_ready := false
var pending_loads: Dictionary = {}

func _ready() -> void:
    ai.request_completed.connect(_on_completed)
    ai.request_failed.connect(_on_failed)
    $Margin/Column/Models/PickChat.pressed.connect($ChatModelDialog.popup_centered)
    $Margin/Column/Models/PickEmbedding.pressed.connect($EmbeddingModelDialog.popup_centered)
    $ChatModelDialog.file_selected.connect(_load_chat_model)
    $EmbeddingModelDialog.file_selected.connect(_load_embedding_model)
    _set_domain_ready("chat", false)
    _set_domain_ready("embedding", false)

    var chat_path := OS.get_environment("SALMON_CHAT_MODEL")
    if chat_path.is_empty():
        chat_path = ProjectSettings.globalize_path("res://../models/Qwen3-0.6B-Q4_K_M.gguf")
    var embedding_path := OS.get_environment("SALMON_EMBEDDING_MODEL")
    if embedding_path.is_empty():
        embedding_path = ProjectSettings.globalize_path("res://../../addons/salmon/models/all-MiniLM-L6-v2-Q4_K_M.gguf")
    _load_chat_model(chat_path)
    _load_embedding_model(embedding_path)

func _load_chat_model(path: String) -> void:
    _load_model(path, "chat")

func _load_embedding_model(path: String) -> void:
    _load_model(path, "embedding")

func _load_model(path: String, domain: String) -> void:
    if not FileAccess.file_exists(path):
        _set_status("%s model not found—choose a local GGUF file." % domain.capitalize())
        _set_picker_enabled(domain, true)
        return
    var old_model: int = chat_model if domain == "chat" else embedding_model
    var old_ready: bool = chat_ready if domain == "chat" else embedding_ready
    _set_domain_ready(domain, false)
    _set_picker_enabled(domain, false)
    var threads := _inference_threads()
    var batch_threads := _batch_threads()
    var purpose := "chat" if domain == "chat" else "embedding"
    var settings := {"context": 2048, "threads": threads, "threads_batch": batch_threads, "batch": 256} if domain == "chat" else {"context": 512, "threads": threads, "threads_batch": batch_threads}
    var handle: int = ai.load_model(path, purpose, settings)
    if handle == 0:
        _set_domain_ready(domain, old_ready)
        _set_picker_enabled(domain, true)
        _set_status("Could not queue %s model load." % domain)
        return
    if domain == "chat":
        chat_model = handle
        chat_path_label.text = "Chat/semantic: loading %s…" % path.get_file()
    else:
        embedding_model = handle
        embedding_path_label.text = "Embeddings: loading %s…" % path.get_file()
    pending_loads[handle] = {"domain": domain, "old_model": old_model if old_ready else 0, "path": path}
    _set_status("Loading %s model locally…" % domain)

func _on_completed(_id: int, result: Dictionary) -> void:
    if result.operation != "load":
        return
    var handle: int = result.model
    if not pending_loads.has(handle):
        return
    var pending: Dictionary = pending_loads[handle]
    pending_loads.erase(handle)
    var domain: String = pending.domain
    _set_domain_ready(domain, true)
    _set_picker_enabled(domain, true)
    var filename: String = String(pending.path).get_file()
    if domain == "chat":
        chat_path_label.text = "Chat/semantic: %s" % filename
    else:
        embedding_path_label.text = "Embeddings: %s" % filename
    var old_model: int = pending.old_model
    if old_model > 0 and old_model != handle:
        ai.unload_model(old_model)
    _refresh_status()

func _on_failed(id: int, error: String) -> void:
    if not pending_loads.has(id):
        _set_status("Request %s failed: %s" % [id, error])
        return
    var pending: Dictionary = pending_loads[id]
    pending_loads.erase(id)
    var domain: String = pending.domain
    var old_model: int = pending.old_model
    if domain == "chat":
        chat_model = old_model
        chat_path_label.text = "Chat/semantic: load failed"
    else:
        embedding_model = old_model
        embedding_path_label.text = "Embeddings: load failed"
    _set_domain_ready(domain, old_model > 0)
    _set_picker_enabled(domain, true)
    _set_status("%s model failed: %s" % [domain.capitalize(), error])

func _set_domain_ready(domain: String, ready: bool) -> void:
    if domain == "chat":
        chat_ready = ready
        chat_demo._set_model_ready(ready)
        semantic_demo._set_model_ready(ready)
    else:
        embedding_ready = ready
        embedding_demo._set_model_ready(ready)

func _set_picker_enabled(domain: String, enabled: bool) -> void:
    var button: Button = $Margin/Column/Models/PickChat if domain == "chat" else $Margin/Column/Models/PickEmbedding
    button.disabled = not enabled

func _refresh_status() -> void:
    if chat_ready and embedding_ready:
        _set_status("Ready — chat generates · semantic constrains · embeddings retrieve")
    elif chat_ready:
        _set_status("Chat and semantic ready; choose or wait for an embedding model.")
    elif embedding_ready:
        _set_status("Embeddings ready; choose or wait for a chat model.")

func _set_status(value: String) -> void:
    status.text = value

func _inference_threads() -> int:
    var override := OS.get_environment("SALMON_THREADS").to_int()
    return clampi(override if override > 0 else OS.get_processor_count(), 1, 4)

func _batch_threads() -> int:
    var override := OS.get_environment("SALMON_THREADS_BATCH").to_int()
    return clampi(override if override > 0 else OS.get_processor_count(), 1, 8)
