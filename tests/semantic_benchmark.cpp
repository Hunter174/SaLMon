#include "runtime/runtime.h"
#include <algorithm>
#include <atomic>
#include <chrono>
#include <iomanip>
#include <iostream>
#include <numeric>
#include <stdexcept>
#include <string>
#include <unordered_map>
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
    std::string raw_selected;
    std::string calibrated_selected;
    float raw_margin = 0;
    float calibrated_margin = 0;
    double milliseconds = 0;
    std::vector<float> raw_logits;
};

float score_margin(const std::vector<float> &scores) {
    std::vector<float> sorted = scores;
    std::sort(sorted.begin(), sorted.end(), std::greater<float>());
    return sorted.size() > 1 ? sorted[0] - sorted[1] : 1.0f;
}

std::vector<float> label_priors(salmon::Backend &backend, salmon::Id model, size_t count,
                                std::atomic_bool &cancelled) {
    Request request;
    request.operation = salmon::Operation::Decide;
    request.model = model;
    request.state = "No facts are available. Every candidate is equally suitable and has no advantage.";
    request.question = "Choose among these deliberately equivalent neutral candidates.";
    for (size_t i = 0; i < count; ++i)
        request.options.push_back({"neutral_" + std::to_string(i), "An equivalent neutral candidate with no advantage"});
    return backend.execute(request, cancelled, {}).logits;
}

Trial run_trial(salmon::Backend &backend, salmon::Id model, const Fixture &fixture,
                const std::vector<Option> &options, const std::vector<float> &priors,
                std::atomic_bool &cancelled) {
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
    if (result.scores.size() != options.size() || result.logits.size() != priors.size())
        throw std::runtime_error("Decision returned wrong score count");
    std::vector<float> adjusted;
    for (size_t i = 0; i < result.logits.size(); ++i) adjusted.push_back(result.logits[i] - priors[i]);
    const auto calibrated_scores = salmon::conditional_softmax(adjusted);
    const size_t best = std::max_element(calibrated_scores.begin(), calibrated_scores.end()) - calibrated_scores.begin();
    return {result.choice_id, options[best].id, score_margin(result.scores),
            score_margin(calibrated_scores), milliseconds, result.logits};
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

            const auto priors = label_priors(*backend, 1, fixtures().front().options.size(), cancelled);
            int raw_correct = 0, calibrated_correct = 0, ensemble_correct = 0;
            int raw_stable = 0, calibrated_stable = 0;
            int trials = 0;
            double total_ms = 0, ensemble_total_ms = 0;
            std::cout << "\nModel: " << argv[argument] << "\nLabel priors:";
            for (float prior : priors) std::cout << ' ' << std::fixed << std::setprecision(3) << prior;
            std::cout << "\n" << std::left << std::setw(28) << "fixture" << std::setw(15) << "expected"
                      << std::setw(27) << "raw base/rotated" << std::setw(31) << "calibrated base/rotated"
                      << std::setw(18) << "4-way ensemble" << "ensemble ms\n";
            for (const auto &fixture : fixtures()) {
                const Trial base = run_trial(*backend, 1, fixture, fixture.options, priors, cancelled);
                auto rotated = fixture.options;
                std::rotate(rotated.begin(), rotated.begin() + 1, rotated.end());
                const Trial reordered = run_trial(*backend, 1, fixture, rotated, priors, cancelled);
                std::vector<std::vector<Option>> orders = {fixture.options, rotated};
                std::vector<Trial> permutation_trials = {base, reordered};
                for (size_t offset = 2; offset < fixture.options.size(); ++offset) {
                    auto order = fixture.options;
                    std::rotate(order.begin(), order.begin() + offset, order.end());
                    orders.push_back(order);
                    permutation_trials.push_back(run_trial(*backend, 1, fixture, order, priors, cancelled));
                }
                std::unordered_map<std::string, double> logit_sums;
                double fixture_ensemble_ms = 0;
                for (size_t permutation = 0; permutation < permutation_trials.size(); ++permutation) {
                    fixture_ensemble_ms += permutation_trials[permutation].milliseconds;
                    for (size_t position = 0; position < orders[permutation].size(); ++position)
                        logit_sums[orders[permutation][position].id] += permutation_trials[permutation].raw_logits[position];
                }
                const auto ensemble_best = std::max_element(logit_sums.begin(), logit_sums.end(),
                    [](const auto &left, const auto &right) { return left.second < right.second; });
                const std::string ensemble_selected = ensemble_best->first;
                ensemble_correct += ensemble_selected == fixture.expected;
                ensemble_total_ms += fixture_ensemble_ms;
                raw_correct += base.raw_selected == fixture.expected;
                raw_correct += reordered.raw_selected == fixture.expected;
                calibrated_correct += base.calibrated_selected == fixture.expected;
                calibrated_correct += reordered.calibrated_selected == fixture.expected;
                raw_stable += base.raw_selected == reordered.raw_selected;
                calibrated_stable += base.calibrated_selected == reordered.calibrated_selected;
                trials += 2;
                total_ms += base.milliseconds + reordered.milliseconds;
                const std::string raw_pair = base.raw_selected + " / " + reordered.raw_selected;
                const std::string calibrated_pair = base.calibrated_selected + " / " + reordered.calibrated_selected;
                std::cout << std::left << std::setw(28) << fixture.name << std::setw(15) << fixture.expected
                          << std::setw(27) << raw_pair << std::setw(31) << calibrated_pair
                          << std::setw(18) << ensemble_selected << std::fixed << std::setprecision(1)
                          << fixture_ensemble_ms << '\n';
            }
            const int fixture_count = static_cast<int>(fixtures().size());
            auto percent = [](int value, int count) { return 100.0 * value / count; };
            std::cout << "Raw accuracy: " << raw_correct << '/' << trials << " (" << std::setprecision(1)
                      << percent(raw_correct, trials) << "%)\n"
                      << "Raw order stability: " << raw_stable << '/' << fixture_count << " ("
                      << percent(raw_stable, fixture_count) << "%)\n"
                      << "Calibrated accuracy: " << calibrated_correct << '/' << trials << " ("
                      << percent(calibrated_correct, trials) << "%)\n"
                      << "Calibrated order stability: " << calibrated_stable << '/' << fixture_count << " ("
                      << percent(calibrated_stable, fixture_count) << "%)\n"
                      << "Four-position ensemble accuracy: " << ensemble_correct << '/' << fixture_count << " ("
                      << percent(ensemble_correct, fixture_count) << "%)\n"
                      << "Mean decision latency: " << total_ms / trials << " ms/trial"
                      << " (one additional calibration pass per model)\n"
                      << "Mean four-position ensemble latency: " << ensemble_total_ms / fixture_count << " ms/fixture\n"
                      << "Quality report only: no pass threshold is asserted.\n";
        }
        return 0;
    } catch (const std::exception &error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
