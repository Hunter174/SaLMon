#pragma once

#include <atomic>
#include <condition_variable>
#include <cstdint>
#include <deque>
#include <functional>
#include <memory>
#include <mutex>
#include <string>
#include <thread>
#include <unordered_map>
#include <vector>

namespace salmon {
using Id = int64_t;
enum class Operation { Load, Unload, Chat, Decide, Embed };
struct Settings {
    int context = 2048;
    int threads = 4;       // Single-token autoregressive decoding.
    int threads_batch = 4; // Prompt/embedding batch evaluation.
    int batch = 256;
    int gpu_layers = 0; // CPU default; -1 requests full offload if compiled/available.
    bool mmap = true;
};
struct Message { std::string role, content; };
struct Option { std::string id, description; };
struct Request {
    Id id = 0;
    Id model = 0;
    Operation operation = Operation::Chat;
    Settings settings;
    std::string path, purpose = "chat";
    std::vector<Message> messages;
    std::string state, question;
    std::vector<Option> options;
    std::vector<std::string> texts;
    int max_tokens = 128;
    bool stream = false;
};
struct Result {
    std::string text, choice_id, model_path, purpose, finish_reason;
    std::string model_name, architecture, model_description, pooling;
    uint64_t model_bytes = 0, parameters = 0;
    int training_context = 0, embedding_dimensions = 0;
    bool has_chat_template = false;
    std::vector<std::string> option_ids;
    std::vector<float> logits, scores;
    std::vector<std::vector<float>> embeddings;
    int prompt_tokens = 0, generated_tokens = 0;
    double elapsed_ms = 0, prompt_ms = 0, generation_ms = 0, time_to_first_token_ms = 0;
};
struct Event {
    enum class Kind { Token, Completed, Failed };
    Kind kind = Kind::Completed;
    Id id = 0, model = 0;
    Operation operation = Operation::Chat;
    Result result;
    std::string error;
};
using Stream = std::function<void(const std::string &)>;
class Backend {
public:
    virtual ~Backend() = default;
    virtual Result execute(const Request &, std::atomic_bool &, const Stream &) = 0;
};
std::unique_ptr<Backend> make_llama_backend();
std::vector<float> conditional_softmax(const std::vector<float> &logits);
void validate_options(const std::vector<Option> &options);
// Return the complete UTF-8 prefix length, leaving a possible incomplete codepoint.
size_t utf8_prefix_length(const std::string &bytes);

// Owns all backend state on one worker. No Godot objects cross this boundary.
class Runtime {
public:
    explicit Runtime(std::unique_ptr<Backend> backend = make_llama_backend());
    ~Runtime();
    Runtime(const Runtime &) = delete;
    Runtime &operator=(const Runtime &) = delete;
    Id submit(Request request); // 0 means capacity exhausted or shutdown.
    bool cancel(Id id);
    void cancel_all();
    std::vector<Event> poll();
    static constexpr size_t capacity = 32;
private:
    struct Work { Request request; std::shared_ptr<std::atomic_bool> cancelled; };
    std::unique_ptr<Backend> backend_;
    std::mutex mutex_;
    std::condition_variable wake_;
    std::deque<Work> queue_;
    std::vector<Event> events_;
    std::unordered_map<Id, std::shared_ptr<std::atomic_bool>> pending_;
    Id next_id_ = 1;
    bool stopping_ = false;
    std::thread worker_;
    void run();
    void push(Event event);
};
} // namespace salmon
