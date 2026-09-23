extends SceneTree

# Run in a temporary project containing the freshly built addon.
# Optional SALMON_CHAT_MODEL / SALMON_EMBEDDING_MODEL environment variables
# enable real-model checks without committing or downloading model weights.
var runtime: Node
var completed: Dictionary = {}
var failures: Dictionary = {}
var chunks: Dictionary = {}
var terminal_counts: Dictionary = {}
var failed := false

func _initialize() -> void:
    run.call_deferred()

func check(condition: bool, message: String) -> void:
    if not condition:
        failed = true
        push_error(message)

func on_completed(id: int, result: Dictionary) -> void:
    completed[id] = result
    terminal_counts[id] = terminal_counts.get(id, 0) + 1

func on_failed(id: int, error: String) -> void:
    failures[id] = error
    terminal_counts[id] = terminal_counts.get(id, 0) + 1

func on_token(id: int, text: String) -> void:
    chunks[id] = chunks.get(id, "") + text

func wait_for(id: int) -> void:
    var deadline := Time.get_ticks_msec() + 60000
    while not terminal_counts.has(id) and Time.get_ticks_msec() < deadline:
        await process_frame
    check(terminal_counts.get(id, 0) == 1, "Request %s must have exactly one terminal event" % id)

func run() -> void:
    check(ClassDB.class_exists("Salmon"), "Salmon class was not registered")
    check(ClassDB.class_exists("LLMNode"), "LLMNode compatibility class was not registered")
    if failed:
        quit(1)
        return
    runtime = ClassDB.instantiate("Salmon")
    root.add_child(runtime)
    runtime.request_completed.connect(on_completed)
    runtime.request_failed.connect(on_failed)
    runtime.token_received.connect(on_token)

    var missing: int = runtime.load_model("user://missing-salmon-test-model.gguf", "chat")
    check(missing > 0, "Load request must return an ID")
    await wait_for(missing)
    check(failures.has(missing), "Missing file must fail asynchronously")

    var invalid: int = runtime.chat(999999, [{"role": "user", "content": "Hello"}], 8)
    await wait_for(invalid)
    check(failures.has(invalid), "Unknown model handle must fail asynchronously")

    var cancelled: int = runtime.load_model("user://another-missing-model.gguf", "chat")
    check(runtime.cancel(cancelled), "Cancellation must accept an outstanding ID")
    await wait_for(cancelled)
    check(failures.has(cancelled), "Cancelled or invalid load must not succeed")

    var chat_path := OS.get_environment("SALMON_CHAT_MODEL")
    if chat_path.is_empty():
        chat_path = ProjectSettings.globalize_path("res://../models/Qwen3-0.6B-Q4_K_M.gguf")
    if not FileAccess.file_exists(chat_path):
        chat_path = ""
    if not chat_path.is_empty():
        var model: int = runtime.load_model(chat_path, "chat", {"context": 1024, "threads": 2})
        await wait_for(model)
        check(completed.has(model), "Chat model load failed: " + str(failures.get(model, "")))
        if completed.has(model):
            var message := [{"role": "user", "content": "Say hello."}]
            var chat_id: int = runtime.chat(model, message, 16, true)
            await wait_for(chat_id)
            check(completed.has(chat_id), "Chat generation failed")
            if completed.has(chat_id):
                check(completed[chat_id].text == chunks.get(chat_id, ""), "Stream and final text differ")
            var decision: int = runtime.decide(model, "The player is hurt.", "Which action restores health?", [
                {"id": "heal", "description": "Drink a healing potion"},
                {"id": "attack", "description": "Attack the enemy"}
            ])
            await wait_for(decision)
            check(completed.has(decision), "Decision failed")
            if completed.has(decision):
                var scores: PackedFloat32Array = completed[decision].scores
                check(scores.size() == 2 and abs(scores[0] + scores[1] - 1.0) < 0.00001, "Invalid decision scores")
            var unload: int = runtime.unload_model(model)
            await wait_for(unload)
            var after_unload: int = runtime.chat(model, message, 8)
            await wait_for(after_unload)
            check(failures.has(after_unload), "Unloaded handle must fail")

    var embedding_path := OS.get_environment("SALMON_EMBEDDING_MODEL")
    if embedding_path.is_empty():
        embedding_path = ProjectSettings.globalize_path("res://../../addons/salmon/models/all-MiniLM-L6-v2-Q4_K_M.gguf")
    if not FileAccess.file_exists(embedding_path):
        embedding_path = ""
    if not embedding_path.is_empty():
        var model: int = runtime.load_model(embedding_path, "embedding", {"context": 512, "threads": 2})
        await wait_for(model)
        check(completed.has(model), "Embedding load failed")
        if completed.has(model):
            var embedding: int = runtime.embed(model, PackedStringArray(["A fish swims.", "A fish swims."]))
            await wait_for(embedding)
            check(completed.has(embedding), "Embedding request failed")
            if completed.has(embedding):
                var vectors: Array = completed[embedding].embeddings
                check(vectors.size() == 2 and vectors[0].size() > 0, "Missing embedding vectors")
            runtime.unload_model(model)

    runtime.queue_free()
    await process_frame
    # Destruction while work is queued/active must not leave callbacks into freed nodes.
    for i in range(8):
        var transient: Node = ClassDB.instantiate("Salmon")
        root.add_child(transient)
        transient.load_model(chat_path if not chat_path.is_empty() else "user://missing.gguf", "chat")
        transient.queue_free()
    await process_frame

    var legacy: Node = ClassDB.instantiate("LLMNode")
    root.add_child(legacy)
    check(legacy.has_method("initialize") and legacy.has_signal("stream_token"), "Legacy API missing")
    legacy.queue_free()
    await process_frame
    print("SaLMoN Godot smoke tests: ", "FAILED" if failed else "PASSED")
    quit(1 if failed else 0)
