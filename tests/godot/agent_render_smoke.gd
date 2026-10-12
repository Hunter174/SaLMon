extends Node2D

# Optional interactive Windows frame-responsiveness probe, not an NPC game benchmark.
# Run WITHOUT --headless in a temporary Godot 4.4.1 project with the staged addon,
# SALMON_CHAT_MODEL pointing to an external chat GGUF, and this scene as main scene.
# Draws 400 rectangles each frame, submits 8 concurrent decision requests to one model.
var ai: Node
var load_id := 0
var pending: Dictionary = {}
var finished := 0
var started_ms := 0
var baseline: Array[float] = []
var under_load: Array[float] = []
var active := false
var verified := false
var phase := 0.0
var last_frame_us := 0

func percentile(values: Array[float], fraction: float) -> float:
    values.sort()
    return values[clampi(ceili(values.size() * fraction) - 1, 0, values.size() - 1)]

func _ready() -> void:
    var path := OS.get_environment("SALMON_CHAT_MODEL")
    if not FileAccess.file_exists(path):
        push_error("SALMON_CHAT_MODEL must point to an external GGUF")
        get_tree().quit(1)
        return
    if DisplayServer.get_name() == "headless":
        push_error("Render probe must not run headless")
        get_tree().quit(1)
        return
    Engine.max_fps = 60
    ai = ClassDB.instantiate("Salmon")
    if not ai is Node:
        push_error("Salmon is not registered as Node")
        get_tree().quit(1)
        return
    add_child(ai)
    ai.request_completed.connect(_on_completed)
    ai.request_failed.connect(_on_failed)
    load_id = ai.load_model(path, "chat", {"context": 1024, "batch": 256, "threads": 4, "threads_batch": 8, "gpu_layers": 0})
    if load_id == 0:
        push_error("Model load submission failed")
        get_tree().quit(1)

func _process(delta: float) -> void:
    var now_us := Time.get_ticks_usec()
    var wall_ms := float(now_us - last_frame_us) / 1000.0 if last_frame_us != 0 else 0.0
    last_frame_us = now_us
    phase += delta
    queue_redraw()
    if active:
        under_load.append(wall_ms)
        if Time.get_ticks_msec() - started_ms > 120000:
            push_error("Rendered burst timed out")
            get_tree().quit(1)
    elif baseline.size() < 120 and wall_ms > 0.0:
        baseline.append(wall_ms)
    if finished == 8 and not verified and under_load.size() > 5:
        verified = true
        print("RENDER_PROBE display=", DisplayServer.get_name(), " rectangles=400 baseline_frames=", baseline.size(), " load_frames=", under_load.size(),
            " baseline_p95_ms=", snappedf(percentile(baseline, 0.95), 0.1),
            " load_p95_ms=", snappedf(percentile(under_load, 0.95), 0.1),
            " load_max_ms=", snappedf(under_load.max(), 0.1),
            " load_over_33ms=", under_load.filter(func(x): return x > 33.3).size())
        get_tree().quit()

func _draw() -> void:
    for i in range(400):
        var x := float(i % 25) * 42.0 + sin(phase) * 10.0
        var y := float(i / 25) * 38.0
        draw_rect(Rect2(Vector2(x, y), Vector2(35, 30)), Color(0.2 + float(i % 6) * 0.1, 0.3, 0.5))

func _on_completed(id: int, result: Dictionary) -> void:
    if id == load_id:
        var states := [
            "Hesta the merchant sees a verified past favor; the customer asks for a modest discount.",
            "Ivo the guard sees an expired pass; a visitor asks for entry.",
            "Mara the medic sees a bleeding ally and a stable ally; one bandage is available.",
            "Fen the scout sees an ambush near the road; the caravan has a safe alternative route.",
            "Sol the quartermaster owes food to hungry villagers; a guest asks for the only rations.",
            "Neri the courier holds a sealed letter; a stranger asks to see its contents.",
            "Oren hears an untested rumor that the well is poisoned; testing is available.",
            "Lina sees a broken bridge with unclear blame; the repair crew can secure it."
        ]
        var options := [
            [{"id": "discount", "description": "Offer a modest discount"}, {"id": "ignore", "description": "Ignore the favor"}],
            [{"id": "admit", "description": "Admit without a pass"}, {"id": "check", "description": "Verify a valid pass"}],
            [{"id": "urgent", "description": "Treat severe bleeding"}, {"id": "minor", "description": "Treat the stable ally"}],
            [{"id": "warn", "description": "Warn and reroute the caravan"}, {"id": "charge", "description": "Charge blindly"}],
            [{"id": "reserve", "description": "Reserve promised food"}, {"id": "give", "description": "Give it to the guest"}],
            [{"id": "keep", "description": "Protect the sealed letter"}, {"id": "show", "description": "Show the stranger"}],
            [{"id": "test", "description": "Request a water test"}, {"id": "guess", "description": "Present rumor as fact"}],
            [{"id": "secure", "description": "Secure bridge and investigate"}, {"id": "blame", "description": "Guess who is guilty"}]
        ]
        started_ms = Time.get_ticks_msec()
        active = true
        for i in range(8):
            var request: int = ai.decide(result.model, states[i], "What should this agent do next?", options[i])
            if request == 0:
                push_error("Burst queue unexpectedly full")
                get_tree().quit(1)
                return
            pending[request] = true
        return
    if not pending.has(id) or not result.has("queue_ms") or result.queue_ms < 0.0 or result.scores.size() != 2:
        push_error("Invalid rendered-burst result")
        get_tree().quit(1)
        return
    pending.erase(id)
    finished += 1
    if finished == 8:
        print("RENDER_PROBE last_agent_response_ms=", Time.get_ticks_msec() - started_ms,
            " last_agent_queue_ms=", snappedf(result.queue_ms, 0.1))

func _on_failed(_id: int, error: String) -> void:
    push_error("Rendered burst failed: " + error)
    get_tree().quit(1)
