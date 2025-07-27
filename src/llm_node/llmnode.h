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
    llama_model* model = nullptr;
    llama_context* ctx = nullptr;
    llama_sampler* sampler = nullptr;
    const llama_vocab* vocab = nullptr;

    int context_length = 2048;

protected:
    static void _bind_methods();
    std::string model_path;

public:
    LLMNode();
    ~LLMNode();

    godot::String generate(const godot::Array& messages_gd);
};

#endif // LLMNODE_H
