#include "runtime/runtime.h"
#include <cmath>
#include <iostream>
#include <stdexcept>
using namespace salmon;
int main(int argc, char **argv) {
    if (argc != 3) { std::cerr << "Usage: salmon_model_smoke <chat|embedding> <model.gguf>\n"; return 2; }
    try {
        auto backend = make_llama_backend();
        std::atomic_bool cancelled{false};
        Request load; load.operation = Operation::Load; load.model = 1; load.path = argv[2]; load.purpose = argv[1];
        load.settings.context = load.purpose == "embedding" ? 512 : 2048;
        backend->execute(load, cancelled, {});
        Request request; request.model = 1;
        if (load.purpose == "embedding") {
            request.operation = Operation::Embed; request.texts = {"A small fish swims.", "A small fish swims.", "The mountain is tall."};
            const auto result = backend->execute(request, cancelled, {});
            if (result.embeddings.size() != 3 || result.embeddings[0].empty()) throw std::runtime_error("Missing embeddings");
            double norm = 0, similarity = 0;
            for (size_t i = 0; i < result.embeddings[0].size(); ++i) {
                norm += result.embeddings[0][i] * result.embeddings[0][i];
                similarity += result.embeddings[0][i] * result.embeddings[1][i];
            }
            if (std::abs(norm - 1) > 1e-4 || std::abs(similarity - 1) > 1e-4) throw std::runtime_error("Embedding normalization/repeatability failure");
            std::cout << "Embedding dimensions: " << result.embeddings[0].size() << '\n';
        } else {
            request.messages = {{"user", "Say hello."}}; request.max_tokens = 16; request.stream = true;
            std::string streamed;
            const auto result = backend->execute(request, cancelled, [&](const std::string &text) { streamed += text; });
            if (result.text.empty() || streamed != result.text) throw std::runtime_error("Chat/stream mismatch");
            const auto repeat = backend->execute(request, cancelled, [](const std::string &) {});
            if (repeat.text != result.text) throw std::runtime_error("Independent requests leaked context");
            std::cout << result.text << '\n';
            request.operation = Operation::Decide; request.state = "The player is injured."; request.question = "Which action restores health?";
            request.options = {{"heal", "Use a healing potion"}, {"attack", "Attack the enemy"}};
            const auto decision = backend->execute(request, cancelled, {});
            if (decision.scores.size() != 2 || std::abs(decision.scores[0] + decision.scores[1] - 1) > 1e-5) throw std::runtime_error("Invalid decision scores");
            std::cout << "Selected " << decision.choice_id << " (not an accuracy assertion)\n";
        }
        request.operation = Operation::Unload;
        backend->execute(request, cancelled, {});
        return 0;
    } catch (const std::exception &error) { std::cerr << error.what() << '\n'; return 1; }
}
