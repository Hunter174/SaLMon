#include "runtime.h"
#include "llama.h"
#include "ggml-backend.h"
#include <algorithm>
#include <cmath>
#include <filesystem>
#include <limits>
#include <stdexcept>
#include <unordered_set>

namespace salmon {
namespace {
using Model = std::unique_ptr<llama_model, decltype(&llama_model_free)>;
using Context = std::unique_ptr<llama_context, decltype(&llama_free)>;
struct Session {
    Model model{nullptr, llama_model_free};
    Context context{nullptr, llama_free}; // destroyed before model
    Settings settings;
    std::string path, purpose;
};
void check_cancel(const std::atomic_bool &cancelled) {
    if (cancelled.load()) throw std::runtime_error("Request cancelled");
}
bool abort_eval(void *data) { return static_cast<std::atomic_bool *>(data)->load(); }
bool load_progress(float, void *data) { return !abort_eval(data); }
struct AbortGuard {
    llama_context *context;
    AbortGuard(llama_context *ctx, std::atomic_bool &cancelled) : context(ctx) {
        llama_set_abort_callback(context, abort_eval, &cancelled);
    }
    ~AbortGuard() { llama_set_abort_callback(context, nullptr, nullptr); }
};
void reset(Session &session) {
    if (auto memory = llama_get_memory(session.context.get())) llama_memory_clear(memory, true);
}
std::vector<llama_token> tokenize(const llama_vocab *vocab, const std::string &text, bool special = true) {
    if (text.size() > 4 * 1024 * 1024) throw std::invalid_argument("Input exceeds 4 MiB limit");
    int count = llama_tokenize(vocab, text.data(), static_cast<int>(text.size()), nullptr, 0, special, special);
    if (count >= 0) throw std::runtime_error("Empty tokenization");
    std::vector<llama_token> tokens(-count);
    count = llama_tokenize(vocab, text.data(), static_cast<int>(text.size()), tokens.data(), static_cast<int>(tokens.size()), special, special);
    if (count < 0) throw std::runtime_error("Tokenization failed");
    tokens.resize(count);
    return tokens;
}
std::string format_chat(Session &session, const std::vector<Message> &messages) {
    if (messages.empty()) throw std::invalid_argument("Messages cannot be empty");
    const char *chat_template = llama_model_chat_template(session.model.get(), nullptr);
    if (!chat_template) throw std::runtime_error("Model has no supported chat template");
    std::vector<llama_chat_message> native;
    size_t bytes = 0;
    for (const auto &message : messages) {
        if (message.role != "system" && message.role != "user" && message.role != "assistant")
            throw std::invalid_argument("Supported roles: system, user, assistant");
        bytes += message.content.size();
        if (bytes > 4 * 1024 * 1024) throw std::invalid_argument("Messages exceed 4 MiB limit");
        native.push_back({message.role.c_str(), message.content.c_str()});
    }
    int size = llama_chat_apply_template(chat_template, native.data(), native.size(), true, nullptr, 0);
    if (size <= 0) throw std::runtime_error("Chat template unsupported by pinned llama.cpp");
    std::vector<char> buffer(size + 1);
    int written = llama_chat_apply_template(chat_template, native.data(), native.size(), true, buffer.data(), static_cast<int>(buffer.size()));
    if (written < 0 || written > size) throw std::runtime_error("Chat template formatting failed");
    std::string formatted(buffer.data(), written);
    // Qwen3-style templates expose enable_thinking, but llama_chat_apply_template
    // cannot pass template arguments. A /no_think request therefore needs the
    // same empty reasoning prefix the template emits when that flag is false.
    // This saves hidden autoregressive work and puts decision labels immediately
    // after the assistant prefix. Other templates are left untouched.
    const bool no_think = std::any_of(messages.begin(), messages.end(), [](const Message &message) {
        return message.content.find("/no_think") != std::string::npos;
    });
    if (no_think && std::string(chat_template).find("enable_thinking") != std::string::npos)
        formatted += "<think>\n\n</think>\n\n";
    return formatted;
}
void prefill(Session &session, std::vector<llama_token> &tokens, std::atomic_bool &cancelled) {
    if (tokens.empty() || tokens.size() > llama_n_ctx(session.context.get())) throw std::invalid_argument("Prompt exceeds context capacity");
    reset(session);
    const size_t batch_size = llama_n_batch(session.context.get());
    for (size_t pos = 0; pos < tokens.size(); pos += batch_size) {
        check_cancel(cancelled);
        auto batch = llama_batch_get_one(tokens.data() + pos, static_cast<int>(std::min(batch_size, tokens.size() - pos)));
        const int status = llama_decode(session.context.get(), batch);
        check_cancel(cancelled);
        if (status != 0) throw std::runtime_error("Prompt decode failed (code " + std::to_string(status) + ")");
    }
}
std::string token_piece(const llama_vocab *vocab, llama_token token) {
    std::vector<char> buffer(64);
    int count = llama_token_to_piece(vocab, token, buffer.data(), static_cast<int>(buffer.size()), 0, false);
    if (count < 0) {
        buffer.resize(-count);
        count = llama_token_to_piece(vocab, token, buffer.data(), static_cast<int>(buffer.size()), 0, false);
    }
    if (count < 0) throw std::runtime_error("Token decoding failed");
    return std::string(buffer.data(), count);
}
class LlamaBackend final : public Backend {
    std::unordered_map<Id, Session> sessions_;
public:
    Result execute(const Request &request, std::atomic_bool &cancelled, const Stream &stream) override {
        // Lazy init: creating an idle node need not initialize any hardware.
        static std::once_flag initialized;
        std::call_once(initialized, [] { ggml_backend_load_all(); llama_backend_init(); });
        check_cancel(cancelled);
        if (request.operation == Operation::Load) return load(request, cancelled);
        const auto it = sessions_.find(request.model);
        if (it == sessions_.end()) throw std::invalid_argument("Unknown/unloaded model handle");
        auto &session = it->second;
        Result result;
        result.model_path = session.path; result.purpose = session.purpose;
        if (request.operation == Operation::Unload) {
            sessions_.erase(it);
            return result;
        }
        AbortGuard abort(session.context.get(), cancelled);
        if (request.operation == Operation::Embed) {
            if (session.purpose != "embedding") throw std::invalid_argument("embed requires an embedding model handle");
            embed(session, request, cancelled, result);
        } else {
            if (session.purpose != "chat") throw std::invalid_argument("chat/decide require a chat model handle");
            if (request.operation == Operation::Chat) chat(session, request, cancelled, stream, result);
            else if (request.operation == Operation::Decide) decide(session, request, cancelled, result);
            else throw std::invalid_argument("Unsupported operation");
        }
        check_cancel(cancelled);
        return result;
    }
private:
    Result load(const Request &request, std::atomic_bool &cancelled) {
        const auto &settings = request.settings;
        if (settings.context < 32 || settings.context > 131072 || settings.batch < 1 || settings.batch > settings.context ||
            settings.threads < 1 || settings.threads > 256 || settings.threads_batch < 1 || settings.threads_batch > 256 || settings.gpu_layers < -1)
            throw std::invalid_argument("Invalid context (32–131072), batch (1–context), threads/threads_batch (1–256), or GPU layers (-1 or >=0)");
        if (request.purpose != "chat" && request.purpose != "embedding") throw std::invalid_argument("Purpose must be chat or embedding");
        if (!std::filesystem::is_regular_file(std::filesystem::u8path(request.path)))
            throw std::invalid_argument("Model must be a real filesystem file (not a PCK-only resource)");
        if (settings.gpu_layers != 0 && !llama_supports_gpu_offload())
            throw std::runtime_error("GPU offload unavailable; retry with gpu_layers=0");
        if (sessions_.size() >= 8) throw std::runtime_error("Maximum 8 loaded models per node; unload unused handles");
        Session session;
        session.path = request.path; session.purpose = request.purpose; session.settings = settings;
        auto model_params = llama_model_default_params();
        model_params.n_gpu_layers = settings.gpu_layers == -1 ? 999 : settings.gpu_layers;
        model_params.use_mmap = settings.mmap;
        model_params.progress_callback = load_progress; model_params.progress_callback_user_data = &cancelled;
        session.model.reset(llama_model_load_from_file(request.path.c_str(), model_params));
        check_cancel(cancelled);
        if (!session.model) throw std::runtime_error("GGUF model load failed; check architecture support, file and memory");
        const bool encoder = llama_model_has_encoder(session.model.get());
        const bool decoder = llama_model_has_decoder(session.model.get());
        // In this pinned llama.cpp, BERT uses llama_decode and reports has_decoder.
        // These flags describe the evaluation API, not causal vs bidirectional attention.
        if (request.purpose == "chat" && (encoder || !decoder || !llama_model_chat_template(session.model.get(), nullptr)))
            throw std::invalid_argument("Chat requires a decoder model with a chat template");
        if (request.purpose == "embedding" && encoder && decoder)
            throw std::invalid_argument("Encoder-decoder embeddings are not supported");
        auto params = llama_context_default_params();
        params.n_ctx = settings.context;
        params.n_batch = request.purpose == "embedding" ? settings.context : settings.batch;
        params.n_ubatch = params.n_batch; // Encoder attention requires the complete input in one microbatch.
        params.n_threads = settings.threads; params.n_threads_batch = settings.threads_batch;
        params.embeddings = request.purpose == "embedding";
        params.offload_kqv = settings.gpu_layers != 0;
        params.op_offload = settings.gpu_layers != 0;
        session.context.reset(llama_init_from_model(session.model.get(), params));
        check_cancel(cancelled);
        if (!session.context) throw std::runtime_error("Context allocation failed");
        if (request.purpose == "embedding") {
            const auto pooling = llama_pooling_type(session.context.get());
            if (pooling != LLAMA_POOLING_TYPE_MEAN && pooling != LLAMA_POOLING_TYPE_CLS && pooling != LLAMA_POOLING_TYPE_LAST)
                throw std::invalid_argument("Embedding model requires mean, CLS, or last sequence pooling");
        }
        Result result; result.model_path = session.path; result.purpose = session.purpose;
        if (!sessions_.emplace(request.model, std::move(session)).second) throw std::invalid_argument("Model handle already loaded");
        return result;
    }
    void chat(Session &session, const Request &request, std::atomic_bool &cancelled, const Stream &stream, Result &result) {
        if (request.max_tokens < 1 || request.max_tokens > 32768) throw std::invalid_argument("max_tokens must be 1–32768");
        const auto *vocab = llama_model_get_vocab(session.model.get());
        auto tokens = tokenize(vocab, format_chat(session, request.messages));
        if (tokens.size() + request.max_tokens > llama_n_ctx(session.context.get()))
            throw std::invalid_argument("Prompt plus requested output exceeds context capacity");
        prefill(session, tokens, cancelled);
        std::unique_ptr<llama_sampler, decltype(&llama_sampler_free)> sampler(llama_sampler_init_greedy(), llama_sampler_free);
        if (!sampler) throw std::runtime_error("Sampler allocation failed");
        std::string pending;
        result.finish_reason = "length";
        for (int i = 0; i < request.max_tokens; ++i) {
            check_cancel(cancelled);
            llama_token token = llama_sampler_sample(sampler.get(), session.context.get(), -1);
            if (llama_vocab_is_eog(vocab, token)) { result.finish_reason = "stop"; break; }
            pending += token_piece(vocab, token);
            const size_t length = utf8_prefix_length(pending);
            if (length) {
                const auto chunk = pending.substr(0, length);
                result.text += chunk;
                if (request.stream) stream(chunk);
                pending.erase(0, length);
            }
            if (i + 1 < request.max_tokens) {
                auto batch = llama_batch_get_one(&token, 1);
                const int status = llama_decode(session.context.get(), batch);
                check_cancel(cancelled);
                if (status != 0) throw std::runtime_error("Generation decode failed");
            }
        }
        // Do not emit an incomplete UTF-8 sequence when a token limit cuts it short.
        if (!pending.empty()) {
            result.text += "\xef\xbf\xbd";
            if (request.stream) stream("\xef\xbf\xbd");
        }
    }
    void decide(Session &session, const Request &request, std::atomic_bool &cancelled, Result &result) {
        validate_options(request.options);
        if (request.state.empty() || request.question.empty()) throw std::invalid_argument("State and question cannot be empty");
        std::string content = "/no_think\nChoose the best option using the state as evidence. Reply with only the option letter.\nState:\n" + request.state +
            "\nQuestion:\n" + request.question + "\nOptions:\n";
        for (size_t i = 0; i < request.options.size(); ++i)
            content += std::string(1, static_cast<char>('A' + i)) + ": " + request.options[i].description + "\n";
        const auto prompt = format_chat(session, {{"user", content}});
        const auto *vocab = llama_model_get_vocab(session.model.get());
        auto tokens = tokenize(vocab, prompt);
        std::vector<llama_token> labels;
        std::unordered_set<llama_token> distinct;
        for (size_t i = 0; i < request.options.size(); ++i) {
            const auto label = std::string(1, static_cast<char>('A' + i));
            auto candidate = tokenize(vocab, prompt + label);
            if (candidate.size() != tokens.size() + 1 || !std::equal(tokens.begin(), tokens.end(), candidate.begin()) ||
                !distinct.insert(candidate.back()).second)
                throw std::runtime_error("Option labels are not distinct single-token continuations for this template/tokenizer");
            labels.push_back(candidate.back()); result.option_ids.push_back(request.options[i].id);
        }
        prefill(session, tokens, cancelled);
        const float *logits = llama_get_logits_ith(session.context.get(), -1);
        if (!logits) throw std::runtime_error("Model returned no logits");
        for (auto token : labels) result.logits.push_back(logits[token]);
        result.scores = conditional_softmax(result.logits);
        const auto best = std::max_element(result.scores.begin(), result.scores.end()) - result.scores.begin();
        result.choice_id = result.option_ids[best];
    }
    void embed(Session &session, const Request &request, std::atomic_bool &cancelled, Result &result) {
        if (request.texts.empty() || request.texts.size() > 128) throw std::invalid_argument("embed requires 1–128 texts");
        const auto *vocab = llama_model_get_vocab(session.model.get());
        const int dimensions = llama_model_n_embd(session.model.get());
        for (const auto &text : request.texts) {
            check_cancel(cancelled);
            if (text.empty()) throw std::invalid_argument("Embedding text cannot be empty");
            auto tokens = tokenize(vocab, text);
            if (tokens.size() > llama_n_ctx(session.context.get()) || tokens.size() > llama_n_batch(session.context.get()))
                throw std::invalid_argument("Embedding input exceeds context/batch capacity");
            reset(session);
            auto batch = llama_batch_get_one(tokens.data(), static_cast<int>(tokens.size()));
            const int status = llama_model_has_encoder(session.model.get())
                ? llama_encode(session.context.get(), batch) : llama_decode(session.context.get(), batch);
            check_cancel(cancelled);
            if (status != 0) throw std::runtime_error("Encoder evaluation failed");
            const float *values = llama_get_embeddings_seq(session.context.get(), 0);
            if (!values) throw std::runtime_error("Model returned no pooled sequence embedding");
            std::vector<float> embedding(values, values + dimensions);
            double norm = 0;
            for (float value : embedding) {
                if (!std::isfinite(value)) throw std::runtime_error("Non-finite embedding");
                norm += static_cast<double>(value) * value;
            }
            if (norm <= 0) throw std::runtime_error("Zero-length embedding");
            norm = std::sqrt(norm);
            for (float &value : embedding) value = static_cast<float>(value / norm);
            result.embeddings.push_back(std::move(embedding));
        }
    }
};
} // namespace
std::unique_ptr<Backend> make_llama_backend() { return std::make_unique<LlamaBackend>(); }
} // namespace salmon
