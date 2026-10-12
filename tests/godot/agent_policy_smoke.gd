extends SceneTree
const Policy = preload("res://agent_policy.gd")
const Scheduler = preload("res://agent_scheduler.gd")
var failed := 0
var revisions := {"medic": 1, "guard": 1, "scout": 1, "smith": 1}
var results: Array[Dictionary] = []

class FakeRuntime extends Node:
    signal request_completed(id: int, result: Dictionary)
    signal request_failed(id: int, error: String)
    var next_id := 100
    var started: Array[String] = []
    var canceled: Array[int] = []
    var delay_s := 0.05
    var bad_output := false
    func decide(_model: int, state: String, _question: String, options: Array) -> int:
        var id := next_id
        next_id += 1
        started.append(state)
        get_tree().create_timer(delay_s).timeout.connect(func():
            if not canceled.has(id):
                request_completed.emit(id, {"choice_id": "injected" if bad_output else options[0].id,
                    "elapsed_ms": delay_s * 1000.0, "queue_ms": 0.0}))
        return id
    func cancel(id: int) -> bool:
        canceled.append(id)
        request_failed.emit(id, "canceled")
        return true

func check(condition: bool, explanation: String) -> void:
    if not condition:
        failed += 1
        push_error(explanation)

func option(id: String, requires: Dictionary = {}, costs: Dictionary = {}) -> Dictionary:
    return {"id": id, "description": id, "enabled": true, "requires": requires, "costs": costs}

func _initialize() -> void:
    call_deferred("run")

func run() -> void:
    var facts := {"inventory": {"food": 4}, "reserved": {"food": 4}, "favor_verified": false,
        "pass_valid": false, "urgent_patient": "B"}
    var choices := [option("give", {}, {"food": 1}), option("grant", {"favor_verified": true}),
        option("admit", {"pass_valid": true}), option("treat_a", {"urgent_patient": "A"}),
        option("treat_b", {"urgent_patient": "B"}), option("protect")]
    var gated: Dictionary = Policy.filter_actions(facts, choices, ["treat_a", "treat_b"])
    check(gated.options.size() == 1 and gated.options[0].id == "treat_b", "Authoritative triage failed")
    gated = Policy.filter_actions(facts, choices)
    check(gated.options.map(func(o): return o.id) == ["treat_b", "protect"], "Ledger, scarcity or pass gate failed")
    check(Policy.filter_actions(facts, [option("grant", {"missing": false})]).options.is_empty(), "Missing false fact must fail closed")
    check(Policy.filter_actions(facts, [option("x"), option("x")]).has("error"), "Duplicate IDs must be rejected")
    check(Policy.filter_actions(facts, choices, ["unknown"]).has("error"), "Unknown mandatory action must fail closed")
    facts.reserved.food = 3
    facts.favor_verified = true
    facts.pass_valid = true
    check(Policy.filter_actions(facts, choices).options.size() == 5, "Fact reversal should change legal actions")

    var fake := FakeRuntime.new()
    root.add_child(fake)
    var scheduler: Node = Scheduler.new()
    root.add_child(scheduler)
    scheduler.setup(fake, 42, func(agent: String, revision: int) -> bool: return revisions.get(agent, -1) == revision, 500, 2)
    var report := func(r: Dictionary): results.append(r)
    var safe := [option("hold"), option("admit")]
    # Mandatory rule bypasses the model and has a measured immediate latency.
    var immediate: Array[float] = []
    for i in range(1000):
        scheduler.decide("guard", 1, facts, "pass expired", "allow?", safe, ["hold"], "hold", 1,
            func(r: Dictionary): immediate.append(r.elapsed_ms))
    immediate.sort()
    check(immediate.size() == 1000 and immediate[950] < 1.0 and fake.started.is_empty(),
        "Authoritative fast path should have sub-ms p95 and never call the model")
    scheduler.decide("medic", 1, facts, "first", "choose", safe, [], "hold", 1, report)
    scheduler.decide("scout", 1, facts, "low", "choose", safe, [], "hold", 0, report)
    scheduler.decide("smith", 1, facts, "high", "choose", safe, [], "hold", 2, report)
    await create_timer(0.35).timeout
    check(fake.started == ["first", "high", "low"], "Pending priority/FIFO ordering failed")
    check(results.size() == 3 and results.all(func(r): return r.status == "model" and r.choice_id == "hold"),
        "Unexpected fake-model result")
    var before_cache := fake.started.size()
    scheduler.decide("medic", 1, facts, "first", "choose", safe, [], "hold", 1, report)
    check(fake.started.size() == before_cache and results.back().status == "cache", "Same-revision cache did not hit")
    revisions.medic = 2
    scheduler.decide("medic", 2, facts, "first", "choose", safe, [], "hold", 1, report)
    check(fake.started.size() == before_cache + 1, "New revision must bypass cache")
    revisions.medic = 3
    await create_timer(0.07).timeout
    check(results.back().status == "stale" and results.back().choice_id.is_empty(), "Stale result must never be applied")
    fake.bad_output = true
    scheduler.decide("medic", 3, facts, "invalid output", "choose", safe, [], "hold", 1, report)
    await create_timer(0.07).timeout
    check(results.back().status == "invalid_output" and results.back().choice_id == "hold", "Invalid model output must use legal fallback")
    fake.bad_output = false
    fake.delay_s = 0.5
    var canceled_before := fake.canceled.size()
    scheduler.timeout_ms = 180
    scheduler.decide("medic", 3, facts, "slow", "choose", safe, [], "hold", 1, report)
    scheduler.decide("guard", 1, facts, "pending", "choose", safe, [], "hold", 1, report)
    scheduler.decide("scout", 1, facts, "pending2", "choose", safe, [], "hold", 1, report)
    var before_overflow := results.size()
    scheduler.decide("smith", 1, facts, "overflow", "choose", safe, [], "hold", 1, report)
    check(results.size() == before_overflow + 1 and results.back().status == "backpressure" and results.back().choice_id == "hold",
        "Bounded pending queue must return a legal fallback")
    await create_timer(0.22).timeout
    check(results.any(func(r): return r.status == "timeout" and r.choice_id == "hold") and fake.canceled.size() == canceled_before + 1,
        "Expired request must cancel and resolve with legal fallback")
    check(results.back().elapsed_ms < 1000.0, "Timeout fallback exceeded one second")
    var before_no_action := results.size()
    scheduler.decide("medic", 3, facts, "none", "choose", [option("unverified", {"unknown_fact": true})], [], "unverified", 1, report)
    check(results.size() == before_no_action + 1 and results.back().status == "no_action" and results.back().choice_id.is_empty(),
        "No legal action must not invent or execute one")
    scheduler.decide("medic", 3, facts, "bad fallback", "choose", safe, [], "unverified", 1, report)
    check(results.back().status == "invalid_contract" and results.back().choice_id.is_empty(), "Invalid fallback must fail closed")
    if failed == 0:
        print("AGENT_POLICY: PASSED immediate_p95_ms=", immediate[950], " checks=authoritative,priority,cache,stale,invalid,timeout")
    quit(1 if failed else 0)
