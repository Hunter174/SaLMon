extends SceneTree
# Development-only, frozen scenario probe. Never tune a model/prompt on these cases
# and then call their accuracy "held out". SALMON_CHAT_MODEL is an external GGUF.
const Scheduler = preload("res://agent_scheduler.gd")
var revisions := {}
var results: Array[Dictionary] = []
var load_data := {}
var load_error := ""

func option(id: String, desc: String, requires: Dictionary = {}, costs: Dictionary = {}) -> Dictionary:
    return {"id": id, "description": desc, "enabled": true, "requires": requires, "costs": costs}

func _initialize() -> void:
    call_deferred("run")

func run() -> void:
    var path := OS.get_environment("SALMON_CHAT_MODEL")
    if not FileAccess.file_exists(path):
        push_error("SALMON_CHAT_MODEL must be a local external GGUF")
        quit(1)
        return
    var runtime: Node = ClassDB.instantiate("Salmon")
    root.add_child(runtime)
    var load_id: int = runtime.load_model(path, "chat", {"context": 1024, "batch": 256,
        "threads": 4, "threads_batch": 2, "gpu_layers": 0})
    runtime.request_completed.connect(func(id: int, data: Dictionary):
        if id == load_id: load_data = data)
    runtime.request_failed.connect(func(id: int, error: String):
        if id == load_id: load_error = error)
    var until := Time.get_ticks_msec() + 20000
    while load_data.is_empty() and load_error.is_empty() and Time.get_ticks_msec() < until:
        await process_frame
    if load_data.is_empty():
        push_error("Model load failed: " + load_error)
        quit(1)
        return
    var scheduler: Node = Scheduler.new()
    root.add_child(scheduler)
    scheduler.setup(runtime, load_id, func(agent: String, revision: int): return revisions.get(agent, -1) == revision, 970, 2)
    var cases := [
        {"name": "owed_food", "facts": {"inventory": {"food": 4}, "reserved": {"food": 4}},
            "state": "Cook owes four meals, four remain; a guest requests one.", "options": [option("give", "Give guest a meal", {}, {"food": 1}), option("reserve", "Keep all owed meals")], "expected": "reserve", "mandatory": []},
        {"name": "expired_pass", "facts": {"inventory": {}, "reserved": {}, "pass_valid": false},
            "state": "Visitor's signed gate pass expired. They request entry.", "options": [option("admit", "Admit visitor", {"pass_valid": true}), option("hold", "Hold visitor outside")], "expected": "hold", "mandatory": []},
        {"name": "binding_favor", "facts": {"inventory": {}, "reserved": {}, "favor_verified": true},
            "state": "Merchant's signed ledger confirms a binding promise of a discount.", "options": [option("grant", "Honor promised discount", {"favor_verified": true}), option("refuse", "Break the promise")], "expected": "grant", "mandatory": ["grant"]},
        {"name": "critical_patient", "facts": {"inventory": {"dose": 1}, "reserved": {"dose": 0}, "urgent": "B"},
            "state": "Patient B is critical, A is stable. One dose.", "options": [option("a", "Treat A", {"urgent": "A"}, {"dose": 1}), option("b", "Treat B", {"urgent": "B"}, {"dose": 1})], "expected": "b", "mandatory": []},
        {"name": "polite_guest", "facts": {"inventory": {}, "reserved": {}},
            "state": "An innkeeper has plenty of rooms. A tired traveler politely asks for a small price reduction; no prior deal exists.", "options": [option("offer", "Offer a small courtesy discount"), option("decline", "Politely decline a discount")], "expected": "offer", "fallback": "decline", "mandatory": []},
        {"name": "uncertain_tracks", "facts": {"inventory": {}, "reserved": {}},
            "state": "A scout sees ambiguous tracks near a safe campsite. Quiet inspection and waiting are both safe; the caravan wants news soon.", "options": [option("inspect", "Investigate quietly"), option("wait", "Stay at camp")], "expected": "inspect", "fallback": "wait", "mandatory": []},
        {"name": "bridge_mediation", "facts": {"inventory": {}, "reserved": {}},
            "state": "Two neighbors dispute a bridge repair plan. Both options are safe; one invites discussion before deciding.", "options": [option("discuss", "Invite both neighbors to discuss a repair"), option("postpone", "Postpone the discussion")], "expected": "discuss", "fallback": "postpone", "mandatory": []},
        {"name": "verified_recipient", "facts": {"inventory": {}, "reserved": {}, "recipient_valid": true},
            "state": "Courier has verified the intended recipient's signed authorization. The recipient asks to read their letter now.", "options": [option("show", "Show the letter to its verified recipient", {"recipient_valid": true}), option("deliver", "Carry it on to the planned handoff")], "expected": "show", "fallback": "deliver", "mandatory": []}
    ]
    print("AGENT_PROBE model=", load_data.model_name, " timeout_ms=970, one worker, 2 pending")
    var model_choices := 0
    var model_correct := 0
    var correct := 0
    var individual: Array[float] = []
    for c in cases:
        revisions[c.name] = 1
        var previous := results.size()
        scheduler.decide(c.name, 1, c.facts, c.state, "What should this NPC do next?", c.options,
            c.mandatory, c.get("fallback", c.expected), 1, func(r: Dictionary): results.append(r))
        until = Time.get_ticks_msec() + 1500
        while results.size() == previous and Time.get_ticks_msec() < until:
            await process_frame
        if results.size() == previous:
            push_error("Missing response: " + c.name)
            quit(1)
            return
        var r: Dictionary = results.back()
        correct += int(r.choice_id == c.expected)
        model_choices += int(r.status == "model")
        model_correct += int(r.status == "model" and r.choice_id == c.expected)
        individual.append(r.elapsed_ms)
        print("AGENT_PROBE case=", c.name, " status=", r.status, " choice=", r.choice_id,
            " expected=", c.expected, " latency_ms=", snappedf(r.elapsed_ms, 0.1),
            " queue_ms=", snappedf(r.queue_ms if r.has("queue_ms") else 0.0, 0.1))
    # New agent IDs prevent cache hits. The frozen cases are repeated for a burst
    # stress test, NOT treated as new quality evidence.
    var before := results.size()
    for i in range(cases.size()):
        var c: Dictionary = cases[i]
        var agent: String = "burst_" + c.name
        revisions[agent] = 1
        scheduler.decide(agent, 1, c.facts, c.state, "What should this NPC do next?", c.options,
            c.mandatory, c.get("fallback", c.expected), i, func(r: Dictionary): results.append(r))
    until = Time.get_ticks_msec() + 1500
    while results.size() < before + cases.size() and Time.get_ticks_msec() < until:
        await process_frame
    if results.size() < before + cases.size():
        push_error("Burst did not resolve")
        quit(1)
        return
    var burst: Array[Dictionary] = results.slice(before)
    var over_second := burst.filter(func(r): return r.elapsed_ms >= 1000.0).size()
    var sources := {}
    for r in burst:
        sources[r.status] = sources.get(r.status, 0) + 1
    var meets_probe_target: bool = over_second == 0 and individual.max() < 1000.0 and correct == cases.size() and model_correct == 4 and model_choices == 4
    print("AGENT_PROBE summary independent_correct=", correct, "/", cases.size(),
        " independent_model_correct=", model_correct, "/", model_choices, " burst_sources=", sources,
        " burst_over_1s=", over_second, " burst_max_ms=", snappedf(burst.map(func(r): return r.elapsed_ms).max(), 0.1),
        " probe_target=", "MET" if meets_probe_target else "NOT_MET")
    quit(0 if meets_probe_target else 1)
