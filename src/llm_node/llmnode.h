#pragma once
#include "salmon/salmon.h"
#include <unordered_map>

// Compatibility adapter. New projects should use Salmon with explicit handles.
class LLMNode : public Salmon {
    GDCLASS(LLMNode, Salmon);
    int64_t default_model_ = 0;
    std::unordered_map<int64_t, bool> legacy_requests_;
protected:
    static void _bind_methods();
    void on_event(const salmon::Event &event) override;
public:
    void initialize(godot::String path = "", int context_len = 2048);
    void chat_async(const godot::Array &messages, int max_new_tokens = 512);
    void stream_chat_async(const godot::Array &messages, int max_new_tokens = 512);
    int download_model(godot::String custom_url);
};
