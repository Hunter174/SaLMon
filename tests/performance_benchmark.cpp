#include "runtime/runtime.h"
#include <nlohmann/json.hpp>
#include <algorithm>
#include <atomic>
#include <chrono>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

using json = nlohmann::json;
using Clock = std::chrono::steady_clock;

namespace {
struct TimedResult { salmon::Result result; double milliseconds = 0; };

double percentile(std::vector<double> values, double quantile) {
    if (values.empty()) return 0;
    std::sort(values.begin(), values.end());
    const double position = quantile * (values.size() - 1);
    const size_t lower = static_cast<size_t>(position), upper = std::min(lower + 1, values.size() - 1);
    return values[lower] + (values[upper] - values[lower]) * (position - lower);
}

json statistics(const std::vector<double> &values) {
    if (values.empty()) return json::object();
    double sum = 0;
    for (double value : values) sum += value;
    return {{"count", values.size()}, {"min", *std::min_element(values.begin(), values.end())},
            {"p50", percentile(values, .50)}, {"p90", percentile(values, .90)},
            {"p95", percentile(values, .95)}, {"max", *std::max_element(values.begin(), values.end())},
            {"mean", sum / values.size()}};
}

std::string expand_environment(std::string value) {
    size_t start = 0;
    while ((start = value.find("${", start)) != std::string::npos) {
        const size_t end = value.find('}', start + 2);
        if (end == std::string::npos) throw std::invalid_argument("Unclosed environment variable in model path");
        const std::string name = value.substr(start + 2, end - start - 2);
        const char *replacement = std::getenv(name.c_str());
        if (!replacement) throw std::invalid_argument("Environment variable is not set: " + name);
        value.replace(start, end - start + 1, replacement);
        start += std::char_traits<char>::length(replacement);
    }
    return value;
}

salmon::Settings settings_from(const json &model) {
    salmon::Settings settings;
    const json values = model.value("settings", json::object());
    settings.context = values.value("context", settings.context);
    settings.batch = values.value("batch", settings.batch);
    settings.threads = values.value("threads", settings.threads);
    settings.threads_batch = values.value("threads_batch", settings.threads_batch);
    settings.gpu_layers = values.value("gpu_layers", settings.gpu_layers);
    settings.mmap = values.value("mmap", settings.mmap);
    return settings;
}

salmon::Request workload_request(const std::string &operation, const json &workloads, salmon::Id model) {
    salmon::Request request;
    request.model = model;
    const json config = workloads.value(operation, json::object());
    if (operation == "chat") {
        request.operation = salmon::Operation::Chat;
        request.max_tokens = config.value("max_tokens", 32);
        request.stream = true;
        const auto messages = config.value("messages", json::array({{{"role", "user"}, {"content", "Briefly describe a quiet forest camp."}}}));
        for (const auto &message : messages)
            request.messages.push_back({message.at("role").get<std::string>(), message.at("content").get<std::string>()});
    } else if (operation == "decision") {
        request.operation = salmon::Operation::Decide;
        request.state = config.value("state", "The player is injured and has a healing potion.");
        request.question = config.value("question", "Which action best addresses the immediate problem?");
        const auto options = config.value("options", json::array({
            {{"id", "heal"}, {"description", "Use the healing potion"}},
            {{"id", "attack"}, {"description", "Attack a distant wall"}}
        }));
        for (const auto &option : options)
            request.options.push_back({option.at("id").get<std::string>(), option.at("description").get<std::string>()});
    } else if (operation == "embedding") {
        request.operation = salmon::Operation::Embed;
        request.texts = config.value("texts", std::vector<std::string>{
            "The harbor is closed during the storm.", "Ships cannot enter because of severe weather.",
            "The blacksmith sells iron swords."
        });
    } else throw std::invalid_argument("Unknown benchmark purpose: " + operation);
    return request;
}

TimedResult timed_execute(salmon::Backend &backend, const salmon::Request &request, std::atomic_bool &cancelled) {
    const auto start = Clock::now();
    auto result = backend.execute(request, cancelled, [](const std::string &) {});
    return {std::move(result), std::chrono::duration<double, std::milli>(Clock::now() - start).count()};
}

json benchmark_operation(const json &model, const std::filesystem::path &path, const std::string &operation,
                         const json &workloads, int cold_runs, int warmups, int runs, salmon::Id model_id) {
    json output = {{"operation", operation}, {"status", "ok"}};
    const salmon::Settings settings = settings_from(model);
    const std::string load_purpose = operation == "embedding" ? "embedding" : "chat";
    salmon::Request load;
    load.operation = salmon::Operation::Load;
    load.model = model_id;
    load.path = path.u8string();
    load.purpose = load_purpose;
    load.settings = settings;
    salmon::Request request = workload_request(operation, workloads, model_id);
    std::atomic_bool cancelled{false};
    std::vector<double> cold_load, cold_first;
    try {
        for (int i = 0; i < cold_runs; ++i) {
            auto backend = salmon::make_llama_backend();
            cold_load.push_back(timed_execute(*backend, load, cancelled).milliseconds);
            cold_first.push_back(timed_execute(*backend, request, cancelled).milliseconds);
        }
        auto backend = salmon::make_llama_backend();
        const auto session_load_sample = timed_execute(*backend, load, cancelled);
        const double session_load = session_load_sample.milliseconds;
        const auto &metadata = session_load_sample.result;
        output["capabilities"] = {
            {"name", metadata.model_name}, {"architecture", metadata.architecture},
            {"description", metadata.model_description}, {"model_bytes", metadata.model_bytes},
            {"parameters", metadata.parameters}, {"training_context", metadata.training_context},
            {"embedding_dimensions", metadata.embedding_dimensions},
            {"has_chat_template", metadata.has_chat_template}, {"pooling", metadata.pooling}
        };
        for (int i = 0; i < warmups; ++i) timed_execute(*backend, request, cancelled);
        std::vector<double> latency, prompt_ms, first_token_ms, prompt_tps, generation_tps;
        std::vector<int> prompt_tokens, generated_tokens;
        for (int i = 0; i < runs; ++i) {
            auto sample = timed_execute(*backend, request, cancelled);
            latency.push_back(sample.milliseconds);
            prompt_ms.push_back(sample.result.prompt_ms);
            prompt_tokens.push_back(sample.result.prompt_tokens);
            if (sample.result.prompt_ms > 0 && sample.result.prompt_tokens > 0)
                prompt_tps.push_back(1000.0 * sample.result.prompt_tokens / sample.result.prompt_ms);
            if (operation == "chat") {
                first_token_ms.push_back(sample.result.time_to_first_token_ms);
                generated_tokens.push_back(sample.result.generated_tokens);
                if (sample.result.generation_ms > 0)
                    generation_tps.push_back(1000.0 * sample.result.generated_tokens / sample.result.generation_ms);
            }
        }
        output["cold_load_ms"] = statistics(cold_load);
        output["cold_first_inference_ms"] = statistics(cold_first);
        output["warm_latency_ms"] = statistics(latency);
        output["prompt_ms"] = statistics(prompt_ms);
        output["prompt_tokens_per_second"] = statistics(prompt_tps);
        output["session_load_ms"] = session_load;
        output["samples"] = {{"warm_latency_ms", latency}, {"prompt_tokens", prompt_tokens}};
        if (operation == "chat") {
            output["time_to_first_token_ms"] = statistics(first_token_ms);
            output["generation_tokens_per_second"] = statistics(generation_tps);
            output["samples"]["generated_tokens"] = generated_tokens;
        }
    } catch (const std::exception &error) {
        output["status"] = "skipped";
        output["reason"] = error.what();
    }
    return output;
}

std::string platform_name() {
#ifdef _WIN32
    return "windows";
#elif __APPLE__
    return "macos";
#elif __linux__
    return "linux";
#else
    return "unknown";
#endif
}
} // namespace

int main(int argc, char **argv) {
    if (argc < 2 || argc > 3) {
        std::cerr << "Usage: salmon_performance_benchmark <manifest.json> [results.json]\n";
        return 2;
    }
    try {
        const std::filesystem::path manifest_path = std::filesystem::absolute(std::filesystem::u8path(argv[1]));
        std::ifstream input(manifest_path);
        if (!input) throw std::runtime_error("Cannot open benchmark manifest: " + manifest_path.u8string());
        json manifest = json::parse(input);
        if (manifest.value("version", 0) != 1) throw std::invalid_argument("Benchmark manifest version must be 1");
        const int runs = manifest.value("runs", 10), warmups = manifest.value("warmups", 1), cold_runs = manifest.value("cold_runs", 1);
        if (runs < 1 || runs > 10000 || warmups < 0 || cold_runs < 1 || cold_runs > 100)
            throw std::invalid_argument("Expected runs 1-10000, warmups >= 0, and cold_runs 1-100");
        const json workloads = manifest.value("workloads", json::object());
        json report = {
            {"schema_version", 1},
            {"benchmark", "SaLMoN local GGUF performance"},
            {"build_profile", manifest.value("build_profile", "unspecified")},
            {"host", {{"platform", platform_name()}, {"hardware_threads", std::thread::hardware_concurrency()}}},
            {"configuration", {{"runs", runs}, {"warmups", warmups}, {"cold_runs", cold_runs}}},
            {"models", json::array()}
        };
        salmon::Id model_id = 1;
        for (const auto &model : manifest.at("models")) {
            const std::string id = model.at("id").get<std::string>();
            std::filesystem::path path = std::filesystem::u8path(expand_environment(model.at("path").get<std::string>()));
            if (path.is_relative()) path = manifest_path.parent_path() / path;
            path = std::filesystem::absolute(path);
            json result = {{"id", id}, {"path", path.u8string()},
                           {"source", model.value("source", "")}, {"revision", model.value("revision", "")},
                           {"sha256", model.value("sha256", "")}, {"settings", model.value("settings", json::object())},
                           {"operations", json::array()}};
            std::error_code file_error;
            result["file_size_bytes"] = std::filesystem::file_size(path, file_error);
            if (file_error) {
                result["status"] = "error";
                result["reason"] = "Model is not a readable filesystem file";
            } else {
                result["status"] = "ok";
                for (const auto &purpose : model.at("purposes"))
                    result["operations"].push_back(benchmark_operation(model, path, purpose.get<std::string>(),
                                                                        workloads, cold_runs, warmups, runs, model_id++));
            }
            report["models"].push_back(std::move(result));
        }
        const std::string serialized = report.dump(2) + "\n";
        if (argc == 3) {
            std::ofstream output(std::filesystem::u8path(argv[2]));
            if (!output) throw std::runtime_error("Cannot open result file");
            output << serialized;
        } else std::cout << serialized;
        return 0;
    } catch (const std::exception &error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
