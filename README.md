# 🐟 **SaLMoN** – Small Language Models Navigator
> **SaLMoN (Small Language Models Navigator)** is a Godot extension that embeds **llama.cpp** GGUF models directly into your game.  
> It enables **on-device LLM inference** with support for **asynchronous chat** and **token streaming**.

---

## Features
- Load and run **local GGUF** models via llama.cpp
- Support for **async chat generation** (non-blocking)
- Support for **streaming token output**
- Fully usable from GDScript
- Configurable **context length** and **max new tokens**

---
> The only directly supported OS is Windows I have yet to build the binaries for Mac and Linux
---

## Installation

1. **Copy the `salmon` folder** into the `addons` directory of your Godot project.
2. **Obtain a GGUF model**:
    - You can download a small example model from Hugging Face (e.g., `SmolLM2` or `Llama-3.2-1B`).
    - Use your own GGUF-compatible model if desired. (Information on supported GGUF models can be found [llama.cpp](https://github.com/ggerganov/llama.cpp))
3. **Place the model file** at: ``res://addons/salmon/models/model.gguf`` -> the default model name is models.gguf
4. **Ensure the compiled library** (`.dll`, `.so`, or `.dylib` depending on your platform) is located in:
5. Enable the addon in **Project > Project Settings > Plugins**.
---

## Usage

### 1. **Adding the Node**
- Add an `LLMNode` to your scene (from the extension).
- Connect it to your UI as needed.

---

### 2. **Initializing the Model**
```gdscript
@onready var llm_node: LLMNode = $LLMNode

func _ready():
 # Initialize with the default model path and custom context length
 llm_node.initialize("res://addons/salmon/models/model.gguf", 4096)
```
You can also provide any GGUF model path:

```gdscript
llm_node.initialize("res://addons/salmon/models/YourModel.gguf", 2048)
```
---

### 3. Async Chat Example
```gdscript
var messages = [
    {"role": "system", "content": "You are a mystical traveler."},
    {"role": "user", "content": "Tell me a short poem about the stars."}
]

func _ready():
    llm_node.connect("chat_response_ready", Callable(self, "_on_chat_response"))
    llm_node.chat_async(messages, 512)

func _on_chat_response(response: String):
    print("Model Response: ", response)
```
---

### 4. Streaming Response Example
Streaming sends tokens as they are generated via the stream_token signal.
```gdscript
func _ready():
    llm_node.connect("stream_token", Callable(self, "_on_token"))
    llm_node.connect("stream_complete", Callable(self, "_on_complete"))
    llm_node.stream_chat_async(messages, 512)

func _on_token(token: String):
    print(token, "") # Prints tokens progressively

func _on_complete():
    print("Stream finished.")
```
### Parameters

| Parameter       | Description                                          | Default |
|-----------------|------------------------------------------------------|---------|
| `context_len`   | Max context window size (affects memory & history)   | 2048    |
| `max_new_tokens`| Maximum number of tokens generated per response      | 512     |

---

### Example: In-Game Chat UI

```gdscript
extends Node2D
@onready var chat_log: RichTextLabel = $chat_log
@onready var input_box: LineEdit = $input_box
@onready var send_button: Button = $send_button
@onready var llm_node: LLMNode = $LLMNode

var messages = [{"role": "system", "content": "You are a helpful assistant."}]

func _ready():
    llm_node.initialize("res://addons/salmon/models/model.gguf", 4096)
    llm_node.connect("chat_response_ready", Callable(self, "_on_response"))
    send_button.pressed.connect(_on_send_pressed)

func _on_send_pressed():
    var user_input = input_box.text.strip_edges()
    if user_input == "":
        return
    messages.append({"role": "user", "content": user_input})
    chat_log.append_text("\n[b]You:[/b] " + user_input)
    input_box.text = ""
    chat_log.append_text("\n[i]Thinking...[/i]\n")
    llm_node.chat_async(messages, 512)

func _on_response(response: String):
    messages.append({"role": "assistant", "content": response})
    chat_log.append_text("\n[color=yellow][b]LLM:[/b] " + response + "[/color]")
```
---
### 📝 Notes

- **No model is bundled** with this plugin.
- You must provide your own **GGUF** model. Recommended sources:
    - [Unsloth GGUF Models on Hugging Face](https://huggingface.co/unsloth/Llama-3.2-1B-Instruct-GGUF)
- Tested models include:
    - `SmolLM2-135M-Instruct`
    - `Llama-3.2-1B-Instruct`

💡 **Tip:** You can implement **RAG (Retrieval-Augmented Generation)** by injecting external context into the `messages` array before calling `chat_async` or `stream_chat_async`.

---

### 📄 License
This project follows the **same license as [llama.cpp](https://github.com/ggerganov/llama.cpp)**.