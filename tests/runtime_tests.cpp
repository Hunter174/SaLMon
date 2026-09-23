#include "runtime/runtime.h"
#include <chrono>
#include <cmath>
#include <iostream>
#include <limits>
#include <stdexcept>
#include <thread>

using namespace salmon;
void require(bool ok, const char *message) { if (!ok) throw std::runtime_error(message); }
template <class F> void throws(F fn) {
    bool caught = false;
    try { fn(); } catch (const std::exception &) { caught = true; }
    require(caught, "Expected exception");
}
class Fake final : public Backend {
    std::shared_ptr<std::atomic_bool> started_;
public:
    explicit Fake(std::shared_ptr<std::atomic_bool> started = {}) : started_(std::move(started)) {}
    Result execute(const Request &request, std::atomic_bool &cancelled, const Stream &stream) override {
        if (request.path == "throw") throw std::runtime_error("backend failure");
        if (request.path == "wait") {
            if (started_) started_->store(true);
            while (!cancelled.load()) std::this_thread::sleep_for(std::chrono::milliseconds(1));
            throw std::runtime_error("Request cancelled");
        }
        Result result;
        if (request.stream) { stream("hello "); stream("world"); }
        result.text = "done";
        return result;
    }
};
std::vector<Event> wait_events(Runtime &runtime, size_t terminals) {
    std::vector<Event> events;
    const auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(5);
    size_t count = 0;
    while (count < terminals && std::chrono::steady_clock::now() < deadline) {
        for (auto &event : runtime.poll()) {
            if (event.kind != Event::Kind::Token) ++count;
            events.push_back(std::move(event));
        }
        std::this_thread::sleep_for(std::chrono::milliseconds(1));
    }
    require(count == terminals, "Timed out waiting for terminal events");
    return events;
}
void wait_started(const std::shared_ptr<std::atomic_bool> &started) {
    const auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(5);
    while (!started->load() && std::chrono::steady_clock::now() < deadline)
        std::this_thread::sleep_for(std::chrono::milliseconds(1));
    require(started->load(), "Worker did not start active operation");
}
int main() {
    try {
        auto scores = conditional_softmax({10000, 10000, 9999});
        require(std::abs(scores[0] + scores[1] + scores[2] - 1) < 1e-6, "Softmax normalization");
        require(scores[0] == scores[1] && scores[1] > scores[2], "Softmax ordering");
        throws([] { conditional_softmax({}); });
        throws([] { conditional_softmax({std::numeric_limits<float>::infinity()}); });
        throws([] { conditional_softmax({std::numeric_limits<float>::quiet_NaN()}); });
        validate_options({{"a", "A"}, {"b", "B"}});
        throws([] { validate_options({{"a", "A"}}); });
        throws([] { validate_options({{"a", "A"}, {"a", "B"}}); });
        throws([] { validate_options({{"a", "A"}, {"b", ""}}); });
        require(utf8_prefix_length("hello") == 5, "ASCII boundary");
        require(utf8_prefix_length("a\xe2\x82") == 1, "Partial UTF8 boundary");
        require(utf8_prefix_length("\xe2\x82\xac") == 3, "Complete UTF8 boundary");
        require(utf8_prefix_length("\xf0\x9f\x90") == 0, "Partial four-byte UTF8");
        auto started = std::make_shared<std::atomic_bool>(false);
        Runtime runtime(std::make_unique<Fake>(started));
        Request request; request.stream = true;
        const auto first = runtime.submit(request);
        request.path = "throw";
        const auto second = runtime.submit(request);
        auto events = wait_events(runtime, 2);
        std::string streamed; std::vector<Id> finished;
        for (auto &event : events) {
            if (event.kind == Event::Kind::Token) streamed += event.result.text;
            else finished.push_back(event.id);
        }
        require(streamed == "hello world", "Streaming must preserve bytes");
        require(finished == std::vector<Id>{first, second}, "FIFO ordering");
        require(events.back().kind == Event::Kind::Failed, "Backend errors must reach caller");
        require(!runtime.cancel(first), "Consumed requests must be retired");
        request.path = "wait"; request.stream = false;
        const auto active = runtime.submit(request);
        wait_started(started);
        request.path = "";
        const auto queued = runtime.submit(request);
        require(runtime.cancel(queued), "Cancel queued request");
        require(runtime.cancel(active), "Cancel active request");
        events = wait_events(runtime, 2);
        for (const auto &event : events) require(event.kind == Event::Kind::Failed, "Cancelled work must fail");
        request.path = "wait";
        for (size_t i = 0; i < Runtime::capacity; ++i) require(runtime.submit(request) != 0, "Queue capacity unexpectedly reduced");
        require(runtime.submit(request) == 0, "Queue must be bounded including outstanding results");
        runtime.cancel_all();
        wait_events(runtime, Runtime::capacity);
        started->store(false);
        {
            Runtime shutdown(std::make_unique<Fake>(started));
            shutdown.submit(request);
            wait_started(started);
        } // destructor cancels + joins an actually active operation
        auto backend = make_llama_backend();
        std::atomic_bool cancelled{false};
        Request bad; bad.operation = Operation::Load; bad.path = "nonexistent.gguf";
        throws([&] { backend->execute(bad, cancelled, {}); });
        bad.operation = Operation::Chat; bad.model = 123;
        throws([&] { backend->execute(bad, cancelled, {}); });
        std::cout << "All runtime tests passed\n";
        return 0;
    } catch (const std::exception &error) { std::cerr << error.what() << '\n'; return 1; }
}
