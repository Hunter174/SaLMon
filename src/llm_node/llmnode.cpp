#include "llmnode.h"
#include <godot_cpp/variant/utility_functions.hpp>
using namespace godot;

void LLMNode::_bind_methods() {
    ClassDB::bind_method(D_METHOD("initialize", "model_path_override", "context_len"), &LLMNode::initialize, DEFVAL(""), DEFVAL(2048));
    ClassDB::bind_method(D_METHOD("chat_async", "messages", "max_new_tokens"), &LLMNode::chat_async, DEFVAL(512));
    ClassDB::bind_method(D_METHOD("stream_chat_async", "messages", "max_new_tokens"), &LLMNode::stream_chat_async, DEFVAL(512));
    ClassDB::bind_method(D_METHOD("download_model", "custom_url"), &LLMNode::download_model);
    ADD_SIGNAL(MethodInfo("chat_response_ready", PropertyInfo(Variant::STRING, "response")));
    ADD_SIGNAL(MethodInfo("stream_token", PropertyInfo(Variant::STRING, "token")));
    ADD_SIGNAL(MethodInfo("stream_complete"));
}
void LLMNode::initialize(String path, int context_len) {
    UtilityFunctions::push_warning("LLMNode is a compatibility API. Prefer Salmon.load_model(); initialization is now asynchronous.");
    // FIFO ordering guarantees that earlier work finishes before unload/reload.
    if (default_model_) unload_model(default_model_);
    Dictionary settings; settings["context"] = context_len;
    default_model_ = load_model(path.is_empty() ? String("res://addons/salmon/models/model.gguf") : path, "chat", settings);
}
void LLMNode::chat_async(const Array &messages, int max_new_tokens) {
    const auto id = chat(default_model_, messages, max_new_tokens, false);
    if (id) legacy_requests_[id] = false;
}
void LLMNode::stream_chat_async(const Array &messages, int max_new_tokens) {
    const auto id = chat(default_model_, messages, max_new_tokens, true);
    if (id) legacy_requests_[id] = true;
}
int LLMNode::download_model(String) {
    UtilityFunctions::push_error("download_model is disabled: shell downloads were unsafe. Supply a local GGUF; verified opt-in downloads are tracked in issue #7.");
    return ERR_UNAVAILABLE;
}
void LLMNode::on_event(const salmon::Event &event) {
    const auto found = legacy_requests_.find(event.id);
    if (found == legacy_requests_.end()) return;
    const bool streaming = found->second;
    if (event.kind == salmon::Event::Kind::Token) {
        if (streaming) emit_signal("stream_token", String::utf8(event.result.text.c_str()));
        return;
    }
    legacy_requests_.erase(found);
    if (streaming) emit_signal("stream_complete");
    else if (event.kind == salmon::Event::Kind::Completed) emit_signal("chat_response_ready", String::utf8(event.result.text.c_str()));
    // Failures are always delivered via inherited request_failed, never as fake model text.
}
