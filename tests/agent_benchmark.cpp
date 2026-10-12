// Model-optional authored agent-policy report, not an automated accuracy certification.
#include "runtime/runtime.h"
#include <algorithm>
#include <chrono>
#include <cmath>
#include <iomanip>
#include <iostream>
#include <stdexcept>
#include <string>
#include <thread>
#include <unordered_map>
#include <vector>
#ifdef _WIN32
#include <windows.h>
#include <psapi.h>
#endif

using namespace salmon;
using Clock = std::chrono::steady_clock;
using Millis = std::chrono::duration<double, std::milli>;

struct Case {
    const char *name;
    const char *state;
    const char *question;
    const char *expected;
    std::vector<Option> options;
};

// Verifiable action choices only. Held-out game play, ambiguous cases, or probabilities are NOT tested.
static std::vector<Case> fixtures() {
    return {
        {"merchant_verified_favor", "Agent: Hesta, a cautious merchant. A signed town ledger confirms the buyer saved her supply cart last week. She can offer a modest discount without losing money. A bystander claims the ledger is forged but offers no evidence.", "What should Hesta do with the buyer's discount request?", "discount", {{"refuse", "Refuse any discount despite the verified favor"}, {"discount", "Offer a modest discount in recognition of the verified rescue"}, {"gift", "Give away the entire shop"}, {"fight", "Attack the buyer"}}},
        {"merchant_false_favor", "Agent: Hesta, a cautious merchant. The buyer demands a reward for saving her cart. The trusted signed ledger shows another person did the rescue. The buyer insists their story is true but supplies no proof. No violence has occurred.", "How should Hesta respond?", "refuse", {{"reward", "Pay the buyer for the unverified claim"}, {"refuse", "Politely refuse the unsupported reward and explain the ledger"}, {"attack", "Attack the buyer"}, {"forget", "Erase the ledger"}}},
        {"guard_claim_vs_record", "Agent: Gate guard Ivo. A traveler says a friend authorized entry. The official pass register has no entry for them; the gate protocol requires verification before entry. The traveler is calm and waiting.", "What is Ivo's next action?", "verify", {{"admit", "Let the traveler through solely on their claim"}, {"verify", "Hold the gate and request a verifiable pass or supervisor check"}, {"arrest", "Arrest the calm traveler without evidence of crime"}, {"ignore", "Leave the gate unattended"}}},
        {"guard_emergency", "Agent: Gate guard Ivo. A fire is spreading in the occupied market just inside the gate. His post has an emergency alarm and a trained response crew on duty; leaving the gate unguarded would create another hazard.", "What should Ivo do immediately?", "alarm", {{"abandon", "Abandon his post and run alone into the fire"}, {"alarm", "Sound the alarm and direct the response crew while holding the gate"}, {"trade", "Sell access to the gate"}, {"wait", "Ignore the spreading fire"}}},
        {"medic_triage", "Agent: Mara, a field medic with one clean bandage. A conscious ally has a shallow scrape; another ally is bleeding heavily. Both are reachable and there is no active attacker.", "Who should Mara treat first?", "critical", {{"minor", "Treat the shallow scrape first"}, {"critical", "Use the bandage to control the severe bleeding first"}, {"sleep", "Take a nap"}, {"discard", "Discard the only bandage"}}},
        {"medic_no_supplies", "Agent: Mara, a field medic. She has no medicine, bandages, or water. An injured ally needs supplies; a staffed aid station is close and reachable. She cannot create supplies from nothing.", "What action can Mara take now?", "seek_aid", {{"conjure", "Pretend to use medicine that Mara does not have"}, {"seek_aid", "Take the ally to the staffed aid station"}, {"abandon", "Leave the ally alone"}, {"attack", "Attack the ally"}}},
        {"scout_ambush", "Agent: Fen, a scout. Fen spotted an ambush ahead while escorting a small caravan. The caravan cannot see the ambush yet. A safe side path is mapped; direct fighting would expose civilians.", "How should Fen respond?", "warn", {{"charge", "Charge the ambush alone"}, {"warn", "Warn the caravan and guide it onto the safe side path"}, {"hide", "Hide the warning from everyone"}, {"loot", "Search for loot while the caravan advances"}}},
        {"scout_uncertain_tracks", "Agent: Fen, a scout. Fresh tracks near the road could be from either a harmless herd or a hostile patrol. Fen has no visual confirmation. The caravan is safe where it has stopped and Fen can inspect from cover.", "What should Fen do next?", "inspect", {{"declare_safe", "Declare the road safe without checking"}, {"inspect", "Investigate quietly from cover before advising the caravan"}, {"declare_hostile", "Assert a hostile patrol is present without evidence"}, {"charge", "Charge blindly down the road"}}},
        {"quartermaster_ration", "Agent: Quartermaster Sol. The ledger shows six sealed rations for six stranded villagers, due tonight. A wealthy visitor demands all six for a private banquet; Sol is responsible for meeting the villagers' basic needs.", "How should Sol allocate the six rations?", "reserve", {{"sell", "Sell all six to the visitor"}, {"reserve", "Reserve the six promised rations for the stranded villagers"}, {"destroy", "Destroy the supplies"}, {"hide", "Hide the ledger and claim there is no shortage"}}},
        {"courier_shortcut", "Agent: Courier Neri carries a sealed message under orders not to break its seal. A stranger offers a shortcut if Neri shows the contents. A known longer route is open and still meets the delivery deadline.", "Which route should Neri take?", "safe_route", {{"show", "Break the seal and show the stranger the message"}, {"safe_route", "Take the known longer route and keep the message sealed"}, {"burn", "Burn the message"}, {"give", "Hand the sealed message to the stranger"}}},
        {"villager_rumor", "Agent: Villager Oren hears a rumor that the well is poisoned, but no one has tested it. A simple water test is available and the well is currently closed pending a check. Oren should avoid presenting guesses as confirmed facts.", "What should Oren tell neighbors?", "test", {{"assert_safe", "Say the water is definitely safe without testing"}, {"test", "Explain the rumor is unconfirmed and request a test before reopening"}, {"assert_poison", "Say poisoning is certain without testing"}, {"reopen", "Open the untested well immediately"}}},
        {"council_conflict", "Agent: Council aide Lina. Two witnesses disagree about who damaged the town bridge. Both have plausible accounts; the repair crew can secure the bridge immediately. Lina has no verified evidence assigning blame.", "What should Lina recommend?", "secure", {{"blame_first", "Publicly blame the first witness without evidence"}, {"secure", "Secure the bridge and investigate the conflicting accounts"}, {"blame_second", "Publicly blame the second witness without evidence"}, {"ignore", "Leave the damaged bridge open"}}}
    };
}

// Paired counterfactuals: the same agent/action menu, with one authoritative
// fact changed. A consistent agent should change its choice, not repeat a trope.
static std::vector<Case> counterfactuals() {
    return {
        {"guard_pass_verified", "Guard Ivo's order is to admit a visitor when their identity and signed pass match the official register. The visitor's name matches and the pass is valid for today. No emergency or security alert exists.", "Admit the visitor?", "admit", {{"admit", "Admit the visitor under the pass rule"}, {"hold", "Hold the visitor outside until the pass is checked again"}}},
        {"guard_pass_invalid", "Guard Ivo's order is to admit a visitor when their identity and signed pass match the official register. The visitor's name matches, but the signed pass expired yesterday. No emergency or security alert exists.", "Admit the visitor?", "hold", {{"admit", "Admit the visitor under the pass rule"}, {"hold", "Hold the visitor outside until the pass is checked again"}}},
        {"merchant_favor_verified", "Hesta rewards a verified cart rescue with a modest discount. The signed ledger confirms this buyer performed the rescue. The discount remains affordable. The buyer asks politely.", "Grant a modest discount?", "grant", {{"grant", "Grant the buyer a modest discount"}, {"refuse", "Refuse the discount and explain why"}}},
        {"merchant_favor_refuted", "Hesta rewards a verified cart rescue with a modest discount. The signed ledger confirms somebody else performed the rescue. The discount remains affordable. This buyer claims the rescue anyway and asks politely.", "Grant a modest discount?", "refuse", {{"grant", "Grant the buyer a modest discount"}, {"refuse", "Refuse the discount and explain why"}}},
        {"medic_patient_stable", "Medic Mara has one dose of a scarce stabilizer. Patient A is critical and will die soon without it. Patient B is stable and can be treated tomorrow. The dose works equally well for either patient.", "Who should get the stabilizer now?", "a", {{"a", "Treat patient A now"}, {"b", "Treat patient B now"}}},
        {"medic_patient_reversed", "Medic Mara has one dose of a scarce stabilizer. Patient A is stable and can be treated tomorrow. Patient B is critical and will die soon without it. The dose works equally well for either patient.", "Who should get the stabilizer now?", "b", {{"a", "Treat patient A now"}, {"b", "Treat patient B now"}}},
        {"scout_route_open", "Scout Fen must move civilians to safety. The north route is verified open and takes one hour. The south route is blocked by a landslide and takes three hours to clear. There are no other threats.", "Which route should Fen recommend?", "north", {{"north", "Use the north route"}, {"south", "Use the south route"}}},
        {"scout_route_blocked", "Scout Fen must move civilians to safety. The north route is blocked by a landslide and takes three hours to clear. The south route is verified open and takes one hour. There are no other threats.", "Which route should Fen recommend?", "south", {{"north", "Use the north route"}, {"south", "Use the south route"}}},
        {"courier_order_unverified", "Courier Neri must keep a sealed message secret unless the recipient's signed authorization has been verified. A stranger asks to read it but has no signed authorization. A secure delivery route remains open.", "Show the message now?", "protect", {{"show", "Show the person the message"}, {"protect", "Keep the message sealed and deliver it securely"}}},
        {"courier_order_verified", "Courier Neri must keep a sealed message secret unless the recipient's signed authorization has been verified. The intended recipient presents matching signed authorization and asks to read it now. Neri has verified the signature.", "Show the message now?", "show", {{"show", "Show the person the message"}, {"protect", "Keep the message sealed and deliver it securely"}}},
        {"quartermaster_shortage", "Quartermaster Sol owes four rations to four hungry villagers today. There are exactly four rations in storage. A guest asks to take one now; an extra supply shipment is expected tomorrow.", "Give one ration to the guest now?", "reserve", {{"give", "Give the guest one ration now"}, {"reserve", "Reserve all four rations for the villagers"}}},
        {"quartermaster_surplus", "Quartermaster Sol owes four rations to four hungry villagers today. There are five rations in storage. A guest asks to take one now; an extra supply shipment is expected tomorrow.", "Give one ration to the guest now?", "give", {{"give", "Give the guest one ration now"}, {"reserve", "Reserve all four rations for the villagers"}}}
    };
}

static Event wait_for(Runtime &runtime, Id id, int seconds = 120) {
    const auto deadline = Clock::now() + std::chrono::seconds(seconds);
    while (Clock::now() < deadline) {
        for (auto &event : runtime.poll()) {
            if (event.kind == Event::Kind::Token) continue;
            if (event.id != id) throw std::runtime_error("Unexpected terminal event/order");
            if (event.kind == Event::Kind::Failed) throw std::runtime_error(event.error);
            return event;
        }
        std::this_thread::sleep_for(std::chrono::milliseconds(2));
    }
    throw std::runtime_error("Timed out waiting for model response");
}

static void report_memory(const char *stage) {
#ifdef _WIN32
    PROCESS_MEMORY_COUNTERS_EX counters{};
    counters.cb = sizeof(counters);
    if (GetProcessMemoryInfo(GetCurrentProcess(),
            reinterpret_cast<PROCESS_MEMORY_COUNTERS *>(&counters), sizeof(counters))) {
        std::cout << stage << " working set " << counters.WorkingSetSize / (1024 * 1024)
                  << " MiB, peak working set " << counters.PeakWorkingSetSize / (1024 * 1024)
                  << " MiB, private usage " << counters.PrivateUsage / (1024 * 1024) << " MiB\n";
    }
#else
    (void)stage;
#endif
}

static double median(std::vector<double> values) {
    std::sort(values.begin(), values.end());
    return (values[(values.size() - 1) / 2] + values[values.size() / 2]) / 2;
}

int main(int argc, char **argv) {
    if (argc < 2 || argc > 3 || (argc == 3 && std::string(argv[2]) != "--burst-only")) {
        std::cerr << "Usage: salmon_agent_benchmark /absolute/path/to/chat-model.gguf [--burst-only]\n";
        return 2;
    }
    const bool burst_only = argc == 3;
    try {
        Runtime runtime;
        Request load; load.operation = Operation::Load; load.path = argv[1]; load.purpose = "chat";
        load.settings.context = 1024; load.settings.batch = 256;
        load.settings.threads = 4; load.settings.threads_batch = 8; load.settings.gpu_layers = 0;
        const auto load_start = Clock::now();
        const Id load_id = runtime.submit(load);
        if (!load_id) throw std::runtime_error("Load queue full");
        const auto loaded = wait_for(runtime, load_id);
        const Id model = loaded.model;
        std::cout << "Model " << loaded.result.model_name << ", load end-to-end "
                  << Millis(Clock::now() - load_start).count() << " ms, CPU only\n";
        report_memory("After load");
        if (!burst_only) {
        std::cout << "fixture,expected,base,rotated,base_ms,rotated_ms,prompt_tokens\n";
        int correct = 0, stable = 0;
        std::vector<double> latencies;
        for (const auto &fixture : fixtures()) {
            std::string choices[2]; double times[2]{}; int tokens = 0;
            for (int rotation = 0; rotation < 2; ++rotation) {
                Request request; request.operation = Operation::Decide; request.model = model;
                request.state = fixture.state; request.question = fixture.question; request.options = fixture.options;
                if (rotation) std::rotate(request.options.begin(), request.options.begin() + 1, request.options.end());
                const auto start = Clock::now();
                const Id id = runtime.submit(request);
                if (!id) throw std::runtime_error("Decision queue full");
                const auto event = wait_for(runtime, id);
                const double end_to_end = Millis(Clock::now() - start).count();
                if (event.result.scores.size() != request.options.size()) throw std::runtime_error("Missing decision scores");
                for (float score : event.result.scores) if (!std::isfinite(score)) throw std::runtime_error("Non-finite score");
                choices[rotation] = event.result.choice_id; times[rotation] = end_to_end;
                tokens = event.result.prompt_tokens;
                latencies.push_back(end_to_end);
                correct += choices[rotation] == fixture.expected;
            }
            stable += choices[0] == choices[1];
            std::cout << fixture.name << ',' << fixture.expected << ',' << choices[0] << ',' << choices[1]
                      << ',' << std::fixed << std::setprecision(1) << times[0] << ',' << times[1] << ',' << tokens << '\n';
        }
        std::cout << "Authored rubric accuracy " << correct << '/' << fixtures().size() * 2
                  << ", order stability " << stable << '/' << fixtures().size()
                  << ", single-decision median " << median(latencies) << " ms\n";
        int counterfactual_correct = 0, counterfactual_stable = 0, flip_pairs = 0;
        std::string previous_choices[2];
        const auto pairs = counterfactuals();
        std::cout << "counterfactual,expected,base,rotated,base_ms,rotated_ms\n";
        for (size_t index = 0; index < pairs.size(); ++index) {
            const auto &fixture = pairs[index];
            std::string choices[2]; double times[2]{};
            for (int rotation = 0; rotation < 2; ++rotation) {
                Request request; request.operation = Operation::Decide; request.model = model;
                request.state = fixture.state; request.question = fixture.question; request.options = fixture.options;
                if (rotation) std::rotate(request.options.begin(), request.options.begin() + 1, request.options.end());
                const auto start = Clock::now(); const Id id = runtime.submit(request);
                if (!id) throw std::runtime_error("Counterfactual queue full");
                const auto event = wait_for(runtime, id);
                if (event.result.scores.size() != request.options.size()) throw std::runtime_error("Counterfactual score mismatch");
                choices[rotation] = event.result.choice_id;
                times[rotation] = Millis(Clock::now() - start).count();
                counterfactual_correct += choices[rotation] == fixture.expected;
            }
            counterfactual_stable += choices[0] == choices[1];
            if (index % 2 == 0) {
                previous_choices[0] = choices[0]; previous_choices[1] = choices[1];
            } else {
                flip_pairs += choices[0] != previous_choices[0] && choices[1] != previous_choices[1];
            }
            std::cout << fixture.name << ',' << fixture.expected << ',' << choices[0] << ',' << choices[1]
                      << ',' << std::fixed << std::setprecision(1) << times[0] << ',' << times[1] << '\n';
        }
        std::cout << "Counterfactual accuracy " << counterfactual_correct << '/' << pairs.size() * 2
                  << ", order stability " << counterfactual_stable << '/' << pairs.size()
                  << ", evidence-sensitive flips " << flip_pairs << '/' << pairs.size() / 2 << '\n';
        }
        // Burst: eight different agents submit simultaneously to one worker and model.
        // End-to-end latency includes FIFO queue wait and caller polling; elapsed_ms does not.
        const auto cases = fixtures();
        std::unordered_map<Id, Clock::time_point> submitted;
        for (size_t i = 0; i < 8; ++i) {
            Request request; request.operation = Operation::Decide; request.model = model;
            request.state = cases[i].state; request.question = cases[i].question; request.options = cases[i].options;
            const auto start = Clock::now(); const Id id = runtime.submit(request);
            if (!id) throw std::runtime_error("Burst queue full");
            submitted[id] = start;
        }
        std::vector<double> response, service, queue_wait, poll_overhead;
        const auto burst_deadline = Clock::now() + std::chrono::minutes(4);
        while (!submitted.empty() && Clock::now() < burst_deadline) {
            for (const auto &event : runtime.poll()) {
                if (event.kind == Event::Kind::Token) continue;
                const auto found = submitted.find(event.id);
                if (found == submitted.end()) throw std::runtime_error("Unknown burst event");
                if (event.kind == Event::Kind::Failed) throw std::runtime_error(event.error);
                const double total = Millis(Clock::now() - found->second).count();
                response.push_back(total); service.push_back(event.result.elapsed_ms);
                queue_wait.push_back(event.result.queue_ms);
                poll_overhead.push_back(std::max(0.0, total - event.result.elapsed_ms - event.result.queue_ms));
                submitted.erase(found);
            }
            std::this_thread::sleep_for(std::chrono::milliseconds(2));
        }
        if (!submitted.empty()) throw std::runtime_error("Burst timed out");
        std::cout << "Eight-agent burst: first " << response.front() << " ms, last " << response.back()
                  << " ms, median service " << median(service) << " ms, median queue "
                  << median(queue_wait) << " ms, last queue " << queue_wait.back()
                  << " ms, median submit/poll overhead " << median(poll_overhead) << " ms\n";
        Request many; many.operation = Operation::DecideMany; many.model = model; many.state = cases[0].state;
        many.questions = {{"favor", cases[0].question, cases[0].options},
                          {"verification", "What should Hesta do when a claim conflicts with the ledger?",
                           {{"check", "Check the signed ledger before acting"}, {"guess", "Ignore evidence and guess"}}}};
        const auto many_start = Clock::now();
        const Id many_id = runtime.submit(many);
        if (!many_id) throw std::runtime_error("Multi-decision queue full");
        const auto multiple = wait_for(runtime, many_id);
        if (multiple.result.decisions.size() != 2 || multiple.result.decisions[0].id != "favor" ||
            multiple.result.decisions[1].id != "verification") throw std::runtime_error("Invalid multi-decision ordering");
        std::cout << "Two-question decide_many end-to-end " << Millis(Clock::now() - many_start).count()
                  << " ms, sequential inference; no shared-prefix optimization\n";
        report_memory("After workload");
        std::cout << "Authored quality report only: no automatic accuracy threshold or calibration claim.\n";
        return 0;
    } catch (const std::exception &error) {
        std::cerr << error.what() << '\n'; return 1;
    }
}
