#include "llmnode.h"
#include <stdexcept>
#include <vector>
#include <cerrno>   // for errno
#include <cstring>  // for strerror

using namespace godot;

void LLMNode::_bind_methods() {
    ClassDB::bind_method(D_METHOD("generate", "messages"), &LLMNode::generate);
}

LLMNode::LLMNode() {
    ggml_backend_load_all();

    // Get absolute path to the addon directory
    String gd_path = "res://addons/salmon/bin/models/model.gguf";
    String abs_path = ProjectSettings::get_singleton()->globalize_path(gd_path);
    model_path = abs_path.utf8().get_data();
    UtilityFunctions::print("Resolved model path: " + abs_path);

    llama_model_params model_params = llama_model_default_params();
    model_params.n_gpu_layers = 0;

    model = llama_model_load_from_file(model_path.c_str(), model_params);
    if (!model) {
    UtilityFunctions::print("❌ llama_model_load_from_file() failed!");
    UtilityFunctions::print("Resolved path: " + abs_path);
    UtilityFunctions::print("C error (llama): " + String(strerror(errno)));

    FILE* f = fopen(model_path.c_str(), "rb");
    if (!f) {
        UtilityFunctions::print("fopen() also failed");
        UtilityFunctions::print("C error (fopen): " + String(strerror(errno)));
    } else {
        UtilityFunctions::print("✅ fopen() succeeded");
        fclose(f);
    }
}

    llama_context_params ctx_params = llama_context_default_params();
    ctx_params.n_ctx = context_length;

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
}

LLMNode::~LLMNode() {
    if (sampler) llama_sampler_free(sampler);
    if (ctx) llama_free(ctx);
    if (model) llama_model_free(model);
}

String LLMNode::generate(const Array& messages_gd) {
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
    int len = llama_chat_apply_template(tmpl, messages.data(), messages.size(), true, formatted.data(), formatted.size());
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
    for (int i = 0; i < 50; ++i) {
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