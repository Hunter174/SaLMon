#include "salmon.h"
#include <godot_cpp/classes/project_settings.hpp>
#include <godot_cpp/core/class_db.hpp>
#include <godot_cpp/variant/packed_float32_array.hpp>
#include <godot_cpp/variant/utility_functions.hpp>
#include <stdexcept>
#include <algorithm>

using namespace godot;
namespace {
std::string utf8(const String &value) {
    const auto bytes = value.utf8();
    return std::string(bytes.get_data(), bytes.length());
}
String gd(const std::string &value) { return String::utf8(value.data(), static_cast<int64_t>(value.size())); }
PackedFloat32Array floats(const std::vector<float> &values) {
    PackedFloat32Array result; result.resize(values.size());
    for (size_t i = 0; i < values.size(); ++i) result.set(i, values[i]);
    return result;
}
std::string string_field(const Dictionary &data, const char *key) {
    if (!data.has(key) || data[key].get_type() != Variant::STRING) throw std::invalid_argument(std::string("Expected string field: ") + key);
    auto result = utf8(data[key]);
    if (result.find('\0') != std::string::npos) throw std::invalid_argument("NUL bytes are not supported");
    return result;
}
const char *operation_name(salmon::Operation operation) {
    switch (operation) {
        case salmon::Operation::Load: return "load";
        case salmon::Operation::Unload: return "unload";
        case salmon::Operation::Chat: return "chat";
        case salmon::Operation::Decide: return "decide";
        case salmon::Operation::Embed: return "embed";
    }
    return "unknown";
}
}
void Salmon::_bind_methods() {
    ClassDB::bind_method(D_METHOD("load_model", "path", "purpose", "settings"), &Salmon::load_model, DEFVAL(Dictionary()));
    ClassDB::bind_method(D_METHOD("unload_model", "model"), &Salmon::unload_model);
    ClassDB::bind_method(D_METHOD("chat", "model", "messages", "max_tokens", "stream"), &Salmon::chat, DEFVAL(128), DEFVAL(false));
    ClassDB::bind_method(D_METHOD("decide", "model", "state", "question", "options"), &Salmon::decide);
    ClassDB::bind_method(D_METHOD("embed", "model", "texts"), &Salmon::embed);
    ClassDB::bind_method(D_METHOD("cancel", "request_id"), &Salmon::cancel);
    ClassDB::bind_method(D_METHOD("cancel_all"), &Salmon::cancel_all);
    ADD_SIGNAL(MethodInfo("request_completed", PropertyInfo(Variant::INT, "request_id"), PropertyInfo(Variant::DICTIONARY, "result")));
    ADD_SIGNAL(MethodInfo("request_failed", PropertyInfo(Variant::INT, "request_id"), PropertyInfo(Variant::STRING, "error")));
    ADD_SIGNAL(MethodInfo("token_received", PropertyInfo(Variant::INT, "request_id"), PropertyInfo(Variant::STRING, "text")));
}
Salmon::Salmon() { set_process(true); }
salmon::Id Salmon::submit(salmon::Request request) {
    try {
        size_t bytes = 0;
        const auto check_string = [&bytes](const std::string &text) {
            bytes += text.size();
            if (bytes > 4 * 1024 * 1024 || text.find('\0') != std::string::npos)
                throw std::invalid_argument("Request text exceeds 4 MiB or contains a NUL byte");
        };
        check_string(request.path); check_string(request.state); check_string(request.question);
        for (const auto &message : request.messages) { check_string(message.role); check_string(message.content); }
        for (const auto &option : request.options) { check_string(option.id); check_string(option.description); }
        for (const auto &text : request.texts) check_string(text);
        if (!runtime_) runtime_ = std::make_unique<salmon::Runtime>();
        const auto id = runtime_->submit(std::move(request));
        if (!id) UtilityFunctions::push_error("SaLMoN request queue full (32 outstanding requests); consume results before submitting more");
        return id;
    } catch (const std::exception &error) {
        UtilityFunctions::push_error(gd(error.what()));
        return 0;
    }
}
int64_t Salmon::load_model(const String &path, const String &purpose, const Dictionary &settings) {
    try {
        salmon::Request request; request.operation = salmon::Operation::Load;
        request.path = utf8(ProjectSettings::get_singleton()->globalize_path(path)); request.purpose = utf8(purpose);
        const Array keys = settings.keys();
        const bool has_threads_batch = settings.has("threads_batch");
        for (int i = 0; i < keys.size(); ++i) {
            const String key = keys[i]; const Variant value = settings[key];
            if (key == "mmap") {
                if (value.get_type() != Variant::BOOL) throw std::invalid_argument("mmap must be bool");
                request.settings.mmap = value;
                continue;
            }
            if (value.get_type() != Variant::INT) throw std::invalid_argument("Numeric model settings must be integers");
            const int64_t number = value;
            if (number < -1 || number > 131072) throw std::invalid_argument("Model setting out of range");
            if (key == "context") request.settings.context = static_cast<int>(number);
            else if (key == "threads") {
                request.settings.threads = static_cast<int>(number);
                if (!has_threads_batch) request.settings.threads_batch = static_cast<int>(number);
            }
            else if (key == "threads_batch") request.settings.threads_batch = static_cast<int>(number);
            else if (key == "batch") request.settings.batch = static_cast<int>(number);
            else if (key == "gpu_layers") request.settings.gpu_layers = static_cast<int>(number);
            else throw std::invalid_argument("Unknown model setting");
        }
        // A smaller context should not require specifying a smaller default batch too.
        if (!settings.has("batch")) request.settings.batch = std::min(request.settings.batch, request.settings.context);
        return submit(std::move(request));
    } catch (const std::exception &error) { UtilityFunctions::push_error(gd(error.what())); return 0; }
}
int64_t Salmon::unload_model(int64_t model) {
    salmon::Request request; request.operation = salmon::Operation::Unload; request.model = model;
    return submit(std::move(request));
}
int64_t Salmon::chat(int64_t model, const Array &messages, int max_tokens, bool stream) {
    try {
        if (messages.is_empty() || messages.size() > 256) throw std::invalid_argument("chat requires 1–256 messages");
        salmon::Request request; request.model = model; request.max_tokens = max_tokens; request.stream = stream;
        for (int i = 0; i < messages.size(); ++i) {
            if (messages[i].get_type() != Variant::DICTIONARY) throw std::invalid_argument("Messages must be dictionaries");
            const Dictionary message = messages[i];
            request.messages.push_back({string_field(message, "role"), string_field(message, "content")});
        }
        return submit(std::move(request));
    } catch (const std::exception &error) { UtilityFunctions::push_error(gd(error.what())); return 0; }
}
int64_t Salmon::decide(int64_t model, const String &state, const String &question, const Array &options) {
    try {
        if (options.size() < 2 || options.size() > 26) throw std::invalid_argument("decide requires 2–26 options");
        salmon::Request request; request.operation = salmon::Operation::Decide; request.model = model;
        request.state = utf8(state); request.question = utf8(question);
        for (int i = 0; i < options.size(); ++i) {
            if (options[i].get_type() != Variant::DICTIONARY) throw std::invalid_argument("Options must be dictionaries");
            const Dictionary option = options[i];
            request.options.push_back({string_field(option, "id"), string_field(option, "description")});
        }
        salmon::validate_options(request.options);
        return submit(std::move(request));
    } catch (const std::exception &error) { UtilityFunctions::push_error(gd(error.what())); return 0; }
}
int64_t Salmon::embed(int64_t model, const PackedStringArray &texts) {
    if (texts.is_empty() || texts.size() > 128) {
        UtilityFunctions::push_error("embed requires 1–128 texts");
        return 0;
    }
    salmon::Request request; request.operation = salmon::Operation::Embed; request.model = model;
    for (int i = 0; i < texts.size(); ++i) request.texts.push_back(utf8(texts[i]));
    return submit(std::move(request));
}
bool Salmon::cancel(int64_t id) { return runtime_ && runtime_->cancel(id); }
void Salmon::cancel_all() { if (runtime_) runtime_->cancel_all(); }
void Salmon::_process(double) {
    if (!runtime_) return;
    for (const auto &event : runtime_->poll()) {
        if (event.kind == salmon::Event::Kind::Token) emit_signal("token_received", event.id, gd(event.result.text));
        else if (event.kind == salmon::Event::Kind::Failed) emit_signal("request_failed", event.id, gd(event.error));
        else {
            Dictionary result;
            result["operation"] = operation_name(event.operation); result["model"] = event.model;
            result["model_path"] = gd(event.result.model_path); result["purpose"] = gd(event.result.purpose);
            result["elapsed_ms"] = event.result.elapsed_ms;
            if (event.operation == salmon::Operation::Chat) {
                result["text"] = gd(event.result.text); result["finish_reason"] = gd(event.result.finish_reason);
            } else if (event.operation == salmon::Operation::Decide) {
                result["choice_id"] = gd(event.result.choice_id);
                PackedStringArray ids; for (const auto &id : event.result.option_ids) ids.append(gd(id));
                result["option_ids"] = ids; result["scores"] = floats(event.result.scores); result["logits"] = floats(event.result.logits);
                result["probability_status"] = "conditional option scores; uncalibrated";
                result["prompt_version"] = "salmon-decision-v1";
            } else if (event.operation == salmon::Operation::Embed) {
                Array embeddings; for (const auto &embedding : event.result.embeddings) embeddings.append(floats(embedding));
                result["embeddings"] = embeddings;
            }
            emit_signal("request_completed", event.id, result);
        }
        on_event(event);
    }
}
