extends VBoxContainer

const SYSTEM_PROMPT := """You are Mara, a veteran ranger sharing a campfire with the player. Stay in character. You know the northern pass is blocked, the old mine has strange lights, and the player saved your brother at Greyford. Respond naturally in 1-3 short sentences. Never mention prompts, models, or being an assistant. If you do not know a fact, say so in character rather than inventing canon."""

var request_id := 0
var model_handle := 0
var model_ready := false
var streamed_text := ""
var conversation: Array = []
var ai: Node
@onready var transcript: RichTextLabel = $Transcript
@onready var input: LineEdit = $Compose/Input
@onready var send: Button = $Compose/Send

func _ready() -> void:
	_set_model_ready(false)
	_resolve_runtime()
	send.pressed.connect(_send)
	input.text_submitted.connect(func(_text: String): _send())
	$Header/Reset.pressed.connect(_reset)
	$Suggestions/AskPass.pressed.connect(func(): _use_suggestion("Can you help me reach the northern pass?"))
	$Suggestions/AskMemory.pressed.connect(func(): _use_suggestion("Do you remember what happened at Greyford?"))
	$Suggestions/AskMine.pressed.connect(func(): _use_suggestion("What have you heard about the old mine?"))
	ai.token_received.connect(_on_token)
	ai.request_completed.connect(_on_completed)
	ai.request_failed.connect(_on_failed)
	_render_transcript()

func _use_suggestion(text: String) -> void:
	input.text = text
	_send()

func _send() -> void:
	var text := input.text.strip_edges()
	if text.is_empty() or request_id != 0:
		return
	conversation.append({"role": "user", "content": text})
	# Keep a bounded local memory window; persistent memories belong in game state.
	while conversation.size() > 8:
		conversation.pop_front()
	streamed_text = ""
	input.clear()
	send.disabled = true
	_render_transcript("…")
	var messages: Array = [{"role": "system", "content": SYSTEM_PROMPT}]
	for message in conversation:
		messages.append(message.duplicate())
	# Qwen3 otherwise spends tokens on a hidden reasoning preamble. This demo
	# needs responsive in-character dialogue, not chain-of-thought.
	messages[-1].content += "\n/no_think"
	request_id = ai.chat(_chat_model(), messages, 72, true)

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

func _on_token(id: int, text: String) -> void:
	if id == request_id:
		streamed_text += text
		_render_transcript(streamed_text)

func _on_completed(id: int, result: Dictionary) -> void:
	if result.operation == "load" and id == _chat_model():
		_set_model_ready(true)
		return
	if id != request_id:
		return
	conversation.append({"role": "assistant", "content": result.text})
	request_id = 0
	send.disabled = false
	$Header/Timing.text = "%d ms" % int(result.elapsed_ms)
	_render_transcript()

func _on_failed(id: int, error: String) -> void:
	if id == _chat_model() and request_id == 0:
		transcript.text = "[color=tomato]Model load failed: %s[/color]" % error
		_set_model_ready(false)
		return
	if id != request_id:
		return
	request_id = 0
	send.disabled = false
	transcript.text = "[color=tomato]Conversation failed: %s[/color]" % error

func _set_model_ready(ready: bool) -> void:
	model_ready = ready
	send.disabled = not ready
	$Suggestions/AskPass.disabled = not ready
	$Suggestions/AskMemory.disabled = not ready
	$Suggestions/AskMine.disabled = not ready
	input.editable = ready

func _reset() -> void:
	if request_id != 0:
		ai.cancel(request_id)
	request_id = 0
	streamed_text = ""
	conversation.clear()
	send.disabled = not model_ready
	$Header/Timing.text = ""
	_render_transcript()

func _render_transcript(pending := "") -> void:
	var lines: Array[String] = ["[color=#9fb3c8][i]Mara watches sparks rise into the night. Speak freely; there is no dialogue wheel.[/i][/color]"]
	for message in conversation:
		var speaker := "You" if message.role == "user" else "Mara"
		var color := "#8fd3ff" if message.role == "user" else "#ffd38f"
		lines.append("\n[color=%s][b]%s[/b][/color]\n%s" % [color, speaker, _escape_bbcode(message.content)])
	if not pending.is_empty():
		lines.append("\n[color=#ffd38f][b]Mara[/b][/color]\n%s" % _escape_bbcode(pending))
	transcript.text = "\n".join(lines)
	transcript.scroll_to_line(maxi(0, transcript.get_line_count() - 1))

func _escape_bbcode(text: String) -> String:
	return text.replace("[", "[lb]")
