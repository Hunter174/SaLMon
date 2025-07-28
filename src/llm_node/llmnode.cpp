#ifdef _WIN32
#include <direct.h>
#include <windows.h>
#include <string>
#include <cstring>

inline void make_dir(const char *path) {
    // Convert char* (UTF-8) to wchar_t
    int len = MultiByteToWideChar(CP_UTF8, 0, path, -1, NULL, 0);
    std::wstring wpath(len, L'\0');
    MultiByteToWideChar(CP_UTF8, 0, path, -1, &wpath[0], len);
    _wmkdir(wpath.c_str());
}
#else
#include <sys/stat.h>
#include <cstring>

inline void make_dir(const char *path) {
    mkdir(path, 0755);
}
#endif

#include "llmnode.h"
#include <stdexcept>
#include <vector>
#include <cerrno>
#include <cstring>
#include <chrono>
#include <thread>

using namespace godot;

void LLMNode::_bind_methods() {
    ClassDB::bind_method(D_METHOD("initialize", "model_path_override", "context_len"),
                         &LLMNode::initialize, DEFVAL(""), DEFVAL(2048));
    ClassDB::bind_method(D_METHOD("chat_async", "messages", "max_new_tokens"),
                         &LLMNode::chat_async, DEFVAL(512));
    ClassDB::bind_method(D_METHOD("stream_chat_async", "messages", "max_new_tokens"),
                         &LLMNode::stream_chat_async, DEFVAL(512));
    ClassDB::bind_method(D_METHOD("download_model", "custom_url"), &LLMNode::download_model);

    ADD_SIGNAL(MethodInfo("chat_response_ready", PropertyInfo(Variant::STRING, "response")));
    ADD_SIGNAL(MethodInfo("stream_token", PropertyInfo(Variant::STRING, "token")));
    ADD_SIGNAL(MethodInfo("stream_complete"));
}


LLMNode::LLMNode() {
    ggml_backend_load_all();
    model = nullptr;
    ctx = nullptr;
    sampler = nullptr;
    vocab = nullptr;
    context_length = 2048;
}

LLMNode::~LLMNode() {
    if (sampler) llama_sampler_free(sampler);
    if (ctx) llama_free(ctx);
    if (model) llama_model_free(model);
}

void LLMNode::initialize(godot::String model_path_override, int context_len) {
    godot::String gd_path = model_path_override.is_empty() ?
        "res://addons/salmon/models/model.gguf" : model_path_override;

    godot::String abs_path = ProjectSettings::get_singleton()->globalize_path(gd_path);
    model_path = abs_path.utf8().get_data();
    UtilityFunctions::print("Initializing model from: " + abs_path);

    llama_model_params model_params = llama_model_default_params();
    model = llama_model_load_from_file(model_path.c_str(), model_params);
    if (!model) {
        UtilityFunctions::print("llama_model_load_from_file() failed at: " + abs_path);
        return;
    }

    llama_context_params ctx_params = llama_context_default_params();
    ctx_params.n_ctx = context_len;

    ctx = llama_init_from_model(model, ctx_params);
    if (!ctx) {
        llama_model_free(model);
        UtilityFunctions::print("Failed to create llama_context");
        return;
    }

    sampler = llama_sampler_chain_init(llama_sampler_chain_default_params());
    llama_sampler_chain_add(sampler, llama_sampler_init_temp(0.8f));
    llama_sampler_chain_add(sampler, llama_sampler_init_dist(LLAMA_DEFAULT_SEED));

    vocab = llama_model_get_vocab(model);
    UtilityFunctions::print("Model initialized successfully with context length: " + String::num_int64(context_len));
}

// Internal blocking chat generation
godot::String LLMNode::generate_chat_internal(const godot::Array &messages_gd, int max_new_tokens) {
    if (!ctx || !model || !sampler || !vocab) {
        return "[Model not loaded]";
    }

    std::vector<std::string> roles, contents;
    std::vector<llama_chat_message> messages;
    for (int i = 0; i < messages_gd.size(); ++i) {
        Dictionary msg_dict = messages_gd[i];
        if (!msg_dict.has("role") || !msg_dict.has("content")) continue;
        roles.emplace_back(String(msg_dict["role"]).utf8().get_data());
        contents.emplace_back(String(msg_dict["content"]).utf8().get_data());
        messages.push_back({roles.back().c_str(), contents.back().c_str()});
    }

    const char* tmpl = llama_model_chat_template(model, nullptr);
    if (!tmpl) return "[No chat template found in model]";

    std::vector<char> formatted(4096);
    int len = llama_chat_apply_template(tmpl, messages.data(), messages.size(),
                                        true, formatted.data(), formatted.size());
    if (len < 0) return "[Chat formatting failed]";

    std::string prompt(formatted.data(), len);
    const bool is_first = llama_memory_seq_pos_max(llama_get_memory(ctx), 0) == -1;

    int n_tokens = -llama_tokenize(vocab, prompt.c_str(), prompt.size(), nullptr, 0, is_first, true);
    if (n_tokens <= 0) return "[Tokenization failed]";

    std::vector<llama_token> tokens(n_tokens);
    llama_tokenize(vocab, prompt.c_str(), prompt.size(), tokens.data(), tokens.size(), is_first, true);

    llama_batch batch = llama_batch_get_one(tokens.data(), tokens.size());
    if (llama_decode(ctx, batch) != 0) return "[Decode failed]";

    std::string output;
    for (int i = 0; i < max_new_tokens; ++i) {
        llama_token token = llama_sampler_sample(sampler, ctx, -1);
        if (llama_vocab_is_eog(vocab, token)) break;

        char buf[128];
        int n = llama_token_to_piece(vocab, token, buf, sizeof(buf), 0, true);
        if (n <= 0) break;

        output.append(buf, n);

        llama_batch next = llama_batch_get_one(&token, 1);
        if (llama_decode(ctx, next) != 0) break;
    }

    return String(output.c_str());
}

// Threaded async chat
void LLMNode::chat_async(const godot::Array &messages_gd, int max_new_tokens) {
    std::thread([this, messages_gd, max_new_tokens]() {
        godot::String result = this->generate_chat_internal(messages_gd, max_new_tokens);
        call_deferred("emit_signal", "chat_response_ready", result);
    }).detach();
}

// Internal streaming generator
void LLMNode::generate_stream_internal(const godot::Array &messages_gd, int max_new_tokens) {
    if (!ctx || !model || !sampler || !vocab) {
        call_deferred("emit_signal", "stream_token", "[Model not loaded]");
        call_deferred("emit_signal", "stream_complete");
        return;
    }

    std::vector<std::string> roles, contents;
    std::vector<llama_chat_message> messages;
    for (int i = 0; i < messages_gd.size(); ++i) {
        Dictionary msg_dict = messages_gd[i];
        if (!msg_dict.has("role") || !msg_dict.has("content")) continue;
        roles.emplace_back(String(msg_dict["role"]).utf8().get_data());
        contents.emplace_back(String(msg_dict["content"]).utf8().get_data());
        messages.push_back({roles.back().c_str(), contents.back().c_str()});
    }

    const char* tmpl = llama_model_chat_template(model, nullptr);
    if (!tmpl) {
        call_deferred("emit_signal", "stream_token", "[No chat template found]");
        call_deferred("emit_signal", "stream_complete");
        return;
    }

    std::vector<char> formatted(4096);
    int len = llama_chat_apply_template(tmpl, messages.data(), messages.size(),
                                        true, formatted.data(), formatted.size());
    if (len < 0) {
        call_deferred("emit_signal", "stream_token", "[Chat formatting failed]");
        call_deferred("emit_signal", "stream_complete");
        return;
    }

    std::string prompt(formatted.data(), len);
    const bool is_first = llama_memory_seq_pos_max(llama_get_memory(ctx), 0) == -1;

    int n_tokens = -llama_tokenize(vocab, prompt.c_str(), prompt.size(), nullptr, 0, is_first, true);
    if (n_tokens <= 0) {
        call_deferred("emit_signal", "stream_token", "[Tokenization failed]");
        call_deferred("emit_signal", "stream_complete");
        return;
    }

    std::vector<llama_token> tokens(n_tokens);
    llama_tokenize(vocab, prompt.c_str(), prompt.size(), tokens.data(), tokens.size(), is_first, true);

    llama_batch batch = llama_batch_get_one(tokens.data(), tokens.size());
    if (llama_decode(ctx, batch) != 0) {
        call_deferred("emit_signal", "stream_token", "[Decode failed]");
        call_deferred("emit_signal", "stream_complete");
        return;
    }

    for (int i = 0; i < max_new_tokens; ++i) {
        llama_token token = llama_sampler_sample(sampler, ctx, -1);
        if (llama_vocab_is_eog(vocab, token)) break;

        char buf[128];
        int n = llama_token_to_piece(vocab, token, buf, sizeof(buf), 0, true);
        if (n <= 0) break;

        call_deferred("emit_signal", "stream_token", String::utf8(buf, n));

        llama_batch next = llama_batch_get_one(&token, 1);
        if (llama_decode(ctx, next) != 0) break;
    }

    call_deferred("emit_signal", "stream_complete");
}

// Threaded async streaming chat
void LLMNode::stream_chat_async(const godot::Array &messages_gd, int max_new_tokens) {
    std::thread([this, messages_gd, max_new_tokens]() {
        this->generate_stream_internal(messages_gd, max_new_tokens);
    }).detach();
}

int LLMNode::download_model(godot::String custom_url) {
    String url = custom_url.is_empty() ? String(DEFAULT_MODEL_URL) : custom_url;

    // Ensure models directory exists
    make_dir("addons");
    make_dir("addons/salmon");
    make_dir("addons/salmon/models");


    UtilityFunctions::print("Downloading model from: " + url);
    String output_path = String("\"") + String(model_path.c_str()) + String("\"");
    String cmd = "curl -L --progress-bar \"" + url + "\" -o " + output_path;
    UtilityFunctions::print("Executing: " + cmd);

    auto start_time = std::chrono::steady_clock::now();
    int result = system(cmd.utf8().get_data());
    auto end_time = std::chrono::steady_clock::now();
    auto elapsed = std::chrono::duration_cast<std::chrono::seconds>(end_time - start_time).count();

    if (result == 0) {
        UtilityFunctions::print("Model downloaded successfully in " + String::num_int64(elapsed) + " seconds");
    } else {
        UtilityFunctions::print("Model download failed (exit code " + String::num(result) + ")");
    }

    return result;
}