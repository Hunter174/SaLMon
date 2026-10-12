extends Node
# GAME-side example; one model-assisted request in flight, bounded prioritized pending queue.
# Caller MUST increment agent revision on every policy-relevant change, validate again
# before mutating the simulation, and supply a safe legal fallback (or accept no_action).
const Policy = preload("res://agent_policy.gd")
var runtime: Node
var model: int
var is_current: Callable
var timeout_ms := 900
var max_pending := 4
var pending: Array[Dictionary] = []
var active: Dictionary = {}
var draining := false
var next_ticket := 1
var cache := {}
var cache_order: Array[String] = []
const CACHE_LIMIT := 256

func setup(shared_runtime: Node, shared_model: int, revision_check: Callable, deadline_ms: int = 900, queue_limit: int = 4) -> void:
    assert(runtime == null and shared_runtime != null and revision_check.is_valid())
    runtime = shared_runtime
    model = shared_model
    is_current = revision_check
    timeout_ms = deadline_ms
    max_pending = queue_limit
    runtime.request_completed.connect(_completed)
    runtime.request_failed.connect(_failed)
    set_process(true)

func decide(agent: String, revision: int, facts: Dictionary, state: String, question: String,
        actions: Array, mandatory_ids: Array, fallback_id: String, priority: int, resolved: Callable) -> int:
    var ticket := next_ticket
    next_ticket += 1
    var start_us := Time.get_ticks_usec()
    var gated: Dictionary = Policy.filter_actions(facts, actions, mandatory_ids)
    if gated.has("error"):
        resolved.call({"ticket": ticket, "status": "invalid_contract", "choice_id": "", "elapsed_ms": float(Time.get_ticks_usec() - start_us) / 1000.0, "error": gated.error})
        return ticket
    var options: Array = gated.options
    if options.size() > 26:
        resolved.call({"ticket": ticket, "status": "invalid_contract", "choice_id": "",
            "elapsed_ms": float(Time.get_ticks_usec() - start_us) / 1000.0, "error": "too many admissible actions"})
        return ticket
    var legal_ids := options.map(func(option): return option.id)
    var choice := fallback_id if legal_ids.has(fallback_id) else ""
    if not is_current.call(agent, revision):
        resolved.call({"ticket": ticket, "status": "stale", "choice_id": "", "elapsed_ms": float(Time.get_ticks_usec() - start_us) / 1000.0})
        return ticket
    if options.size() < 2:
        resolved.call({"ticket": ticket, "status": "authoritative" if options.size() == 1 else "no_action",
            "choice_id": options[0].id if options.size() == 1 else "", "elapsed_ms": float(Time.get_ticks_usec() - start_us) / 1000.0})
        return ticket
    if choice.is_empty():
        resolved.call({"ticket": ticket, "status": "invalid_contract", "choice_id": "", "elapsed_ms": float(Time.get_ticks_usec() - start_us) / 1000.0,
            "error": "fallback is not admissible"})
        return ticket
    # Cache key includes policy-filtered options; revised facts must change revision.
    var key := JSON.stringify([agent, revision, state, question, options, fallback_id])
    if cache.has(key):
        resolved.call({"ticket": ticket, "status": "cache", "choice_id": cache[key], "elapsed_ms": float(Time.get_ticks_usec() - start_us) / 1000.0})
        return ticket
    var work := {"ticket": ticket, "agent": agent, "revision": revision, "state": state,
        "question": question, "options": options, "fallback": choice, "priority": priority,
        "start_us": Time.get_ticks_usec(), "key": key, "resolved": resolved, "request_id": 0}
    if not active.is_empty() and pending.size() >= max_pending:
        _finish(work, "backpressure", choice)
        return ticket
    pending.append(work)
    pending.sort_custom(func(a, b): return a.priority > b.priority if a.priority != b.priority else a.ticket < b.ticket)
    _pump()
    return ticket

func _pump() -> void:
    if draining or not active.is_empty() or runtime == null:
        return
    while not pending.is_empty():
        var work: Dictionary = pending.pop_front()
        if _expired(work):
            _finish(work, "timeout", work.fallback)
            continue
        if not is_current.call(work.agent, work.revision):
            _finish(work, "stale", "")
            continue
        active = work
        active.request_id = runtime.decide(model, work.state, work.question, work.options)
        if active.request_id == 0:
            var failed: Dictionary = active
            active = {}
            _finish(failed, "submission_failed", failed.fallback)
            continue
        break

func _process(_delta: float) -> void:
    draining = true
    if not active.is_empty() and (not is_current.call(active.agent, active.revision) or _expired(active)):
        var canceled: Dictionary = active
        active = {}
        runtime.cancel(canceled.request_id)
        _finish(canceled, "timeout", canceled.fallback)
    var examining: Array[Dictionary] = pending
    pending = []
    for work in examining:
        if _expired(work):
            _finish(work, "timeout", work.fallback)
        elif not is_current.call(work.agent, work.revision):
            _finish(work, "stale", "")
        else:
            pending.append(work)
    pending.sort_custom(func(a, b): return a.priority > b.priority if a.priority != b.priority else a.ticket < b.ticket)
    draining = false
    _pump()

func _expired(work: Dictionary) -> bool:
    return Time.get_ticks_usec() - work.start_us >= timeout_ms * 1000

func _finish(work: Dictionary, status: String, choice: String, queue_ms: float = 0.0, service_ms: float = 0.0) -> void:
    # Never apply a stale result or fallback to a newer state revision.
    if not is_current.call(work.agent, work.revision):
        status = "stale"
        choice = ""
    work.resolved.call({"ticket": work.ticket, "status": status, "choice_id": choice,
        "elapsed_ms": float(Time.get_ticks_usec() - work.start_us) / 1000.0,
        "queue_ms": queue_ms, "service_ms": service_ms})

func _completed(id: int, result: Dictionary) -> void:
    if active.is_empty() or active.request_id != id:
        return
    var done: Dictionary = active
    active = {}
    var chosen: String = result.get("choice_id", "")
    var legal: Array = done.options.map(func(option): return option.id)
    var status := "model"
    if _expired(done):
        chosen = done.fallback
        status = "timeout"
    elif not legal.has(chosen):
        chosen = done.fallback
        status = "invalid_output"
    if not is_current.call(done.agent, done.revision):
        chosen = ""
        status = "stale"
    elif status == "model":
        cache[done.key] = chosen
        cache_order.append(done.key)
        if cache_order.size() > CACHE_LIMIT:
            cache.erase(cache_order.pop_front())
    _finish(done, status, chosen, result.get("queue_ms", 0.0), result.get("elapsed_ms", 0.0))
    _pump()

func _failed(id: int, _error: String) -> void:
    if active.is_empty() or active.request_id != id:
        return
    var done: Dictionary = active
    active = {}
    _finish(done, "model_failed", done.fallback)
    _pump()
