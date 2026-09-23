extends VBoxContainer

const RECORDS := [
	{"title": "Guard report: missing caravan", "text": "A merchant wagon vanished north of town. Tracks leave the road toward the abandoned silver mine."},
	{"title": "Herbalist's field notes", "text": "Moonleaf steeped with honey reduces fever. It grows beside cold streams after sunset."},
	{"title": "Blacksmith's ledger", "text": "Hesta bought iron from the Greyford caravan and still owes its driver twelve crowns."},
	{"title": "Miner's final journal", "text": "Blue lights move deep inside the old mine, followed by voices where no workers remain."},
	{"title": "Tavern rumor", "text": "A hooded buyer pays smugglers for stolen trade goods beneath the east bridge at midnight."},
	{"title": "Shrine inscription", "text": "Ring the hilltop bell at dawn to open the pilgrim's sealed path through the northern pass."},
	{"title": "Hunter's map", "text": "Fresh wolf dens lie west of the river. The eastern forest trail is currently safe."}
]

var request_id := 0
var model_handle := 0
var corpus_vectors: Array = []
var ai: Node
@onready var query: LineEdit = $QueryRow/Query
@onready var result: RichTextLabel = $Result

func _ready() -> void:
	_set_model_ready(false)
	_resolve_runtime()
	$QueryRow/Search.pressed.connect(_search)
	query.text_submitted.connect(func(_text: String): _search())
	$Examples/Medicine.pressed.connect(func(): _use_query("What could help someone with a dangerous fever?"))
	$Examples/StolenGoods.pressed.connect(func(): _use_query("Who might be buying cargo stolen from merchants?"))
	$Examples/StrangeLights.pressed.connect(func(): _use_query("Where have people seen unexplained lights underground?"))
	ai.request_completed.connect(_on_completed)
	ai.request_failed.connect(_on_failed)

func _use_query(text: String) -> void:
	query.text = text
	_search()

func _search() -> void:
	var text := query.text.strip_edges()
	if text.is_empty() or request_id != 0:
		return
	var inputs := PackedStringArray([text])
	if corpus_vectors.is_empty():
		for record in RECORDS:
			inputs.append(record.title + ". " + record.text)
		result.text = "[i]Building the lore index and embedding the first question…[/i]"
	else:
		result.text = "[i]Embedding one question against the cached lore index…[/i]"
	request_id = ai.embed(_embedding_model(), inputs)
	$QueryRow/Search.disabled = true

func _resolve_runtime() -> void:
	var scene := get_tree().current_scene
	ai = scene.get_node_or_null("Salmon")
	if ai == null:
		ai = ClassDB.instantiate("Salmon")
		scene.add_child(ai)
		var path := OS.get_environment("SALMON_EMBEDDING_MODEL")
		if path.is_empty():
			path = ProjectSettings.globalize_path("res://../../addons/salmon/models/all-MiniLM-L6-v2-Q4_K_M.gguf")
		model_handle = ai.load_model(path, "embedding", {"context": 512, "threads": _inference_threads(), "threads_batch": _batch_threads()})

func _embedding_model() -> int:
	if model_handle != 0:
		return model_handle
	return get_tree().current_scene.embedding_model

func _inference_threads() -> int:
	var override := OS.get_environment("SALMON_THREADS").to_int()
	return clampi(override if override > 0 else OS.get_processor_count(), 1, 4)

func _batch_threads() -> int:
	var override := OS.get_environment("SALMON_THREADS_BATCH").to_int()
	return clampi(override if override > 0 else OS.get_processor_count(), 1, 8)

func _on_completed(id: int, data: Dictionary) -> void:
	if data.operation == "load" and id == _embedding_model():
		_set_model_ready(true)
		return
	if id != request_id or data.operation != "embed":
		return
	request_id = 0
	$QueryRow/Search.disabled = false
	var vectors: Array = data.embeddings
	var query_vector: PackedFloat32Array = vectors[0]
	var built_index := corpus_vectors.is_empty()
	if built_index:
		for i in RECORDS.size():
			corpus_vectors.append(vectors[i + 1])
	var scores: Array[float] = []
	var ranked: Array[int] = []
	for i in RECORDS.size():
		scores.append(_dot(query_vector, corpus_vectors[i]))
		ranked.append(i)
	ranked.sort_custom(func(a: int, b: int): return scores[a] > scores[b])
	var lines: Array[String] = [
		"[b]Closest memories by meaning[/b]   [color=#9fb3c8]%d dimensions · %d ms[/color]" % [query_vector.size(), int(data.elapsed_ms)],
		"[color=#9fb3c8]%s No keywords, tags, or generated answer were used.[/color]" % ("Lore index built and cached." if built_index else "Reused cached lore vectors."),
        ""
	]
	for rank in mini(4, ranked.size()):
		var index := ranked[rank]
		var accent := "#ffd38f" if rank == 0 else "#b9c8d8"
		lines.append("[color=%s][b]%d. %s[/b]  %.3f[/color]\n%s\n" % [accent, rank + 1, RECORDS[index].title, scores[index], RECORDS[index].text])
	result.text = "\n".join(lines)

func _dot(a: PackedFloat32Array, b: PackedFloat32Array) -> float:
	var total := 0.0
	for i in mini(a.size(), b.size()):
		total += a[i] * b[i]
	return total

func _set_model_ready(ready: bool) -> void:
	$QueryRow/Search.disabled = not ready
	$Examples/Medicine.disabled = not ready
	$Examples/StolenGoods.disabled = not ready
	$Examples/StrangeLights.disabled = not ready
	query.editable = ready

func _on_failed(id: int, error: String) -> void:
	if id == _embedding_model() and request_id == 0:
		result.text = "[color=tomato]Model load failed: %s[/color]" % error
		_set_model_ready(false)
	elif id == request_id:
		request_id = 0
		result.text = "[color=tomato]Search failed: %s[/color]" % error
		$QueryRow/Search.disabled = false
