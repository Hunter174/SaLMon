extends VBoxContainer

const OPTIONS := [
	{"id": "honor_favor", "description": "Honor a verified past favor with a meaningful discount; use when the player invokes help they actually provided."},
	{"id": "negotiate", "description": "Offer a small discount in response to a polite or reasonable request."},
	{"id": "refuse", "description": "Refuse the request without escalating when it is rude, unsupported, or unreasonable."},
	{"id": "call_guards", "description": "Call nearby guards when the player makes a credible threat but has not started attacking."},
	{"id": "fight", "description": "Defend yourself only when the player declares or begins immediate physical violence."}
]
const REACTIONS := {
	"honor_favor": "‘You did save my ore caravan. Twenty percent off—this once.’",
	"negotiate": "‘You ask like a civilized person. I can take ten percent off.’",
	"refuse": "‘My prices are fair. Buy something or clear the counter.’",
	"call_guards": "‘Guards! I have a would-be extortionist in my shop!’",
	"fight": "The blacksmith reaches for the hammer beneath the counter.",
	"ask_clarification": "‘Say plainly what you want before this gets out of hand.’"
}

var request_id := 0
var model_handle := 0
var ai: Node
var trust := 55
var fear := 12
var price_percent := 100
var guards_nearby := true
var caravan_quest_done := true
@onready var speech: LineEdit = $SpeechRow/Speech
@onready var result: RichTextLabel = $Result

func _ready() -> void:
	_set_model_ready(false)
	_resolve_runtime()
	$SpeechRow/Judge.pressed.connect(_run)
	speech.text_submitted.connect(func(_text: String): _run())
	$Presets/Threat.pressed.connect(func(): _try_line("Lower your prices or die."))
	$Presets/Polite.pressed.connect(func(): _try_line("Could you give me a good deal, please?"))
	$Presets/Favor.pressed.connect(func(): _try_line("Remember when I saved your ore caravan? You owe me a fair price."))
	ai.request_completed.connect(_on_completed)
	ai.request_failed.connect(_on_failed)
	_show_state()

func _try_line(text: String) -> void:
	speech.text = text
	_run()

func _run() -> void:
	var player_line := speech.text.strip_edges()
	if player_line.is_empty() or request_id != 0:
		return
	var state := """NPC: Hesta, proud town blacksmith. Trust: %d/100. Fear: %d/100. Current price: %d%%. Guards nearby: %s. Player really completed the ore-caravan quest: %s. Player says exactly: %s""" % [trust, fear, price_percent, guards_nearby, caravan_quest_done, player_line]
	result.text = "[i]Reading the situation and scoring five legal reactions…[/i]"
	request_id = ai.decide(_chat_model(), state,
		"Which single reaction best fits Hesta, the verified facts, and the player's exact words?", OPTIONS)
	$SpeechRow/Judge.disabled = true

func _resolve_runtime() -> void:
	var scene := get_tree().current_scene
	ai = scene.get_node_or_null("Salmon")
	if ai == null:
		ai = ClassDB.instantiate("Salmon")
		scene.add_child(ai)
		var path := OS.get_environment("SALMON_CHAT_MODEL")
		if path.is_empty():
			path = ProjectSettings.globalize_path("res://../models/Qwen3-0.6B-Q4_K_M.gguf")
		model_handle = ai.load_model(path, "chat", {"context": 2048, "threads": _inference_threads(), "threads_batch": _batch_threads(), "batch": 256})

func _chat_model() -> int:
	if model_handle != 0:
		return model_handle
	return get_tree().current_scene.chat_model

func _inference_threads() -> int:
	var override := OS.get_environment("SALMON_THREADS").to_int()
	return clampi(override if override > 0 else OS.get_processor_count(), 1, 4)

func _batch_threads() -> int:
	var override := OS.get_environment("SALMON_THREADS_BATCH").to_int()
	return clampi(override if override > 0 else OS.get_processor_count(), 1, 8)

func _on_completed(id: int, data: Dictionary) -> void:
	if data.operation == "load" and id == _chat_model():
		_set_model_ready(true)
		return
	if id != request_id or data.operation != "decide":
		return
	request_id = 0
	$SpeechRow/Judge.disabled = false
	var ids: PackedStringArray = data.option_ids
	var scores: PackedFloat32Array = data.scores
	var best_index := 0
	var second_score := -1.0
	for i in scores.size():
		if scores[i] > scores[best_index]:
			best_index = i
	for i in scores.size():
		if i != best_index:
			second_score = maxf(second_score, scores[i])
	var selected: String = data.choice_id
	var margin: float = scores[best_index] - second_score
	var used_fallback := margin < 0.08
	if used_fallback:
		selected = "ask_clarification"
	_apply_action(selected)
	_show_state()
	var lines: Array[String] = [
		"[b]Hesta's reaction:[/b] %s" % REACTIONS[selected],
		"[b]Executed action:[/b] %s%s" % [selected, " (safe low-margin fallback)" if used_fallback else ""],
		"[b]Top-two margin:[/b] %.3f   [b]Latency:[/b] %d ms" % [margin, int(data.elapsed_ms)],
		"",
        "[color=#9fb3c8]All candidate scores (conditional, not calibrated):[/color]"
	]
	for i in ids.size():
		lines.append("  %-14s %.3f" % [ids[i], scores[i]])
	result.text = "\n".join(lines)

func _apply_action(action: String) -> void:
	match action:
		"honor_favor":
			price_percent = 80
			trust = mini(100, trust + 5)
		"negotiate":
			price_percent = 90
			trust = mini(100, trust + 1)
		"refuse":
			price_percent = 100
		"call_guards":
			fear = mini(100, fear + 12)
			trust = maxi(0, trust - 15)
		"fight":
			fear = mini(100, fear + 20)
			trust = maxi(0, trust - 30)

func _show_state() -> void:
	$State.text = "TRUST  %d     FEAR  %d     PRICE  %d%%     GUARDS  %s     CARAVAN FAVOR  %s" % [trust, fear, price_percent, "nearby" if guards_nearby else "absent", "verified" if caravan_quest_done else "none"]

func _set_model_ready(ready: bool) -> void:
	$SpeechRow/Judge.disabled = not ready
	$Presets/Threat.disabled = not ready
	$Presets/Polite.disabled = not ready
	$Presets/Favor.disabled = not ready
	speech.editable = ready

func _on_failed(id: int, error: String) -> void:
	if id == _chat_model() and request_id == 0:
		result.text = "[color=tomato]Model load failed: %s[/color]" % error
		_set_model_ready(false)
	elif id == request_id:
		request_id = 0
		result.text = "[color=tomato]Decision failed: %s[/color]" % error
		$SpeechRow/Judge.disabled = false
