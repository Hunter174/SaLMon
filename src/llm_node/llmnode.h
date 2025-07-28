#ifndef LLMNODE_H
#define LLMNODE_H

#include <godot_cpp/classes/node.hpp>
#include <godot_cpp/core/class_db.hpp>
#include <godot_cpp/variant/utility_functions.hpp>
#include <godot_cpp/classes/project_settings.hpp>
#include "llama.h"

class LLMNode : public godot::Node {
    GDCLASS(LLMNode, Node);

private:
    // llama.cpp state
    llama_model* model = nullptr;
    llama_context* ctx = nullptr;
    llama_sampler* sampler = nullptr;
    const llama_vocab* vocab = nullptr;

    // Model configuration
    int context_length = 2048;
    std::string model_path;

    // Default model URL for downloading if none is provided
    const char* DEFAULT_MODEL_URL =
        "https://huggingface.co/unsloth/SmolLM2-135M-Instruct-GGUF/resolve/main/SmolLM2-135M-Instruct-Q4_K_M.gguf?download=true";

protected:
    static void _bind_methods();

    // Internal helpers
    godot::String generate_chat_internal(const godot::Array& messages_gd, int max_new_tokens);
    void generate_stream_internal(const godot::Array& messages_gd, int max_new_tokens);

public:
    // Lifecycle
    LLMNode();
    ~LLMNode();

    // Model initialization
    void initialize(godot::String model_path_override = "", int context_len = 2048);

    // Async chat inference (emits chat_response_ready)
    void chat_async(const godot::Array& messages_gd, int max_new_tokens);

    // Async streaming inference (emits stream_token and stream_complete)
    void stream_chat_async(const godot::Array& messages_gd, int max_new_tokens);

    // Model download helper
    int download_model(godot::String custom_url);
};

#endif // LLMNODE_H