#include "runtime/runtime.h"
#include <algorithm>
#include <atomic>
#include <chrono>
#include <iomanip>
#include <iostream>
#include <numeric>
#include <stdexcept>
#include <string>
#include <vector>

using salmon::Option;
using salmon::Request;
using salmon::Result;

namespace {
struct Fixture {
    const char *name;
    std::string state;
    std::string question;
    std::string expected;
    std::vector<Option> options;
};

std::vector<Fixture> fixtures() {
    return {
        {"heal_when_safe",
         "The player is badly injured, owns a healing potion, and no enemies are nearby.",
         "What is the best immediate action?", "heal",
         {{"heal", "Drink the available healing potion"}, {"attack", "Attack an imaginary enemy"},
          {"discard", "Throw the potion away"}, {"wait", "Wait without treating the injury"}}},
        {"block_heavy_attack",
         "An enemy is visibly winding up a heavy strike. The player has a raised shield and cannot safely interrupt it.",
         "What should the player do next?", "block",
         {{"attack", "Drop the guard and attack"}, {"block", "Brace behind the raised shield"},
          {"loot", "Search the room for treasure"}, {"sleep", "Try to sleep"}}},
        {"unlock_with_lockpick",
         "A locked door blocks the objective. The player has no key, owns a lockpick, and has confirmed the door can be picked.",
         "How should the player open the door?", "pick_lock",
         {{"knock", "Wait for someone to answer"}, {"pick_lock", "Use the lockpick on the lock"},
          {"drink", "Drink a potion"}, {"leave", "Abandon the objective"}}},
        {"blacksmith_polite_request",
         "Hesta is a cautious blacksmith. The customer politely asks for a small discount. There are no debts, favors, or threats.",
         "Which authored reaction best fits?", "negotiate",
         {{"fight", "Start a physical fight"}, {"call_guards", "Call guards about a crime"},
          {"negotiate", "Offer a small reasonable discount"}, {"gift", "Give all merchandise away"}}},
        {"blacksmith_credible_threat",
         "Hesta is a cautious blacksmith. Guards are nearby. The customer says: Lower your prices or I will kill you. No attack has begun.",
         "Which authored reaction best fits?", "call_guards",
         {{"honor_favor", "Reward a verified past favor"}, {"call_guards", "Call the nearby guards about the threat"},
          {"gift", "Give the threatening customer free goods"}, {"ignore", "Pretend no threat was made"}}},
        {"verified_favor",
         "The quest ledger verifies that the player saved Hesta's ore caravan. The player calmly invokes that favor while requesting a discount.",
         "Which authored reaction best fits?", "honor_favor",
         {{"accuse", "Call the player a liar"}, {"honor_favor", "Honor the verified favor with a meaningful discount"},
          {"fight", "Attack the player"}, {"forget", "Ignore the verified history"}}},
        {"false_favor_claim",
         "The quest ledger proves the player never helped Hesta's caravan. The player falsely claims they completed that quest and demands a reward.",
         "Which authored reaction best fits?", "refuse",
         {{"honor_favor", "Reward the claimed favor without checking"}, {"refuse", "Refuse the unsupported demand"},
          {"gift", "Give away the shop"}, {"apologize", "Apologize for a quest that never happened"}}},
        {"fire_emergency",
         "A cooking fire has spread to a curtain. A full water bucket is within reach and the exit remains clear.",
         "What is the best immediate action?", "extinguish",
         {{"sleep", "Go to sleep beside the fire"}, {"extinguish", "Use the water bucket to extinguish the curtain"},
          {"add_fuel", "Add dry wood to the spreading fire"}, {"loot", "Search cabinets while the fire spreads"}}}
    };
}

struct Trial {
    std::string selected;
    float margin = 0;
    double milliseconds = 0;
};

Trial run_trial(salmon::Backend &backend, salmon::Id model, const Fixture &fixture,
                const std::vector<Option> &options, std::atomic_bool &cancelled) {
    Request request;
    request.operation = salmon::Operation::Decide;
    request.model = model;
    request.state = fixture.state;
    request.question = fixture.question;
    request.options = options;
    const auto start = std::chrono::steady_clock::now();
    const Result result = backend.execute(request, cancelled, {});
    const double milliseconds = std::chrono::duration<double, std::milli>(
        std::chrono::steady_clock::now() - start).count();
    if (result.scores.size() != options.size()) throw std::runtime_error("Decision returned wrong score count");
    std::vector<float> sorted = result.scores;
    std::sort(sorted.begin(), sorted.end(), std::greater<float>());
    return {result.choice_id, sorted.size() > 1 ? sorted[0] - sorted[1] : 1.0f, milliseconds};
}
} // namespace

int main(int argc, char **argv) {
    if (argc < 2) {
        std::cerr << "Usage: salmon_semantic_benchmark <chat-model.gguf> [more-models.gguf ...]\n";
        return 2;
    }
    try {
        for (int argument = 1; argument < argc; ++argument) {
            auto backend = salmon::make_llama_backend();
            std::atomic_bool cancelled{false};
            Request load;
            load.operation = salmon::Operation::Load;
            load.model = 1;
            load.path = argv[argument];
            load.purpose = "chat";
            load.settings.context = 2048;
            load.settings.batch = 256;
            load.settings.threads = 4;
            load.settings.threads_batch = 8;
            backend->execute(load, cancelled, {});

            int correct = 0;
            int stable = 0;
            int trials = 0;
            double total_ms = 0;
            std::cout << "\nModel: " << argv[argument] << "\n";
            std::cout << std::left << std::setw(30) << "fixture" << std::setw(18) << "expected"
                      << std::setw(18) << "base" << std::setw(18) << "rotated"
                      << std::setw(12) << "margin" << "ms\n";
            for (const auto &fixture : fixtures()) {
                const Trial base = run_trial(*backend, 1, fixture, fixture.options, cancelled);
                auto rotated = fixture.options;
                std::rotate(rotated.begin(), rotated.begin() + 1, rotated.end());
                const Trial reordered = run_trial(*backend, 1, fixture, rotated, cancelled);
                correct += base.selected == fixture.expected;
                correct += reordered.selected == fixture.expected;
                stable += base.selected == reordered.selected;
                trials += 2;
                total_ms += base.milliseconds + reordered.milliseconds;
                std::cout << std::left << std::setw(30) << fixture.name << std::setw(18) << fixture.expected
                          << std::setw(18) << base.selected << std::setw(18) << reordered.selected
                          << std::setw(12) << std::fixed << std::setprecision(3) << std::min(base.margin, reordered.margin)
                          << std::setprecision(1) << base.milliseconds + reordered.milliseconds << '\n';
            }
            const int fixture_count = static_cast<int>(fixtures().size());
            std::cout << "Accuracy: " << correct << '/' << trials << " (" << std::setprecision(1)
                      << 100.0 * correct / trials << "%)\n"
                      << "Order stability: " << stable << '/' << fixture_count << " ("
                      << 100.0 * stable / fixture_count << "%)\n"
                      << "Mean latency: " << total_ms / trials << " ms/trial\n"
                      << "Quality report only: no pass threshold is asserted.\n";
        }
        return 0;
    } catch (const std::exception &error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
