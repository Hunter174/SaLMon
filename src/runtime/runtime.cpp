#include "runtime.h"
#include <algorithm>
#include <chrono>
#include <cmath>
#include <stdexcept>
#include <unordered_set>

namespace salmon {
std::vector<float> conditional_softmax(const std::vector<float> &logits) {
    if (logits.empty()) throw std::invalid_argument("No option logits");
    for (float x : logits) if (!std::isfinite(x)) throw std::runtime_error("Non-finite option logit");
    const float maximum = *std::max_element(logits.begin(), logits.end());
    std::vector<float> scores;
    double sum = 0;
    for (float x : logits) { scores.push_back(std::exp(x - maximum)); sum += scores.back(); }
    for (float &x : scores) x = static_cast<float>(x / sum);
    return scores;
}
void validate_options(const std::vector<Option> &options) {
    if (options.size() < 2 || options.size() > 26) throw std::invalid_argument("Decisions require 2–26 options");
    std::unordered_set<std::string> ids;
    for (const auto &option : options) {
        if (option.id.empty() || option.description.empty() || !ids.insert(option.id).second)
            throw std::invalid_argument("Options require unique nonempty IDs and nonempty descriptions");
    }
}
size_t utf8_prefix_length(const std::string &bytes) {
    if (bytes.empty()) return 0;
    size_t start = bytes.size() - 1;
    while (start > 0 && (static_cast<unsigned char>(bytes[start]) & 0xc0) == 0x80) --start;
    const auto c = static_cast<unsigned char>(bytes[start]);
    size_t length = c < 0x80 ? 1 : (c & 0xe0) == 0xc0 ? 2 : (c & 0xf0) == 0xe0 ? 3 : (c & 0xf8) == 0xf0 ? 4 : 1;
    return start + length <= bytes.size() ? bytes.size() : start;
}
Runtime::Runtime(std::unique_ptr<Backend> backend) : backend_(std::move(backend)) {
    if (!backend_) throw std::invalid_argument("Backend is required");
    worker_ = std::thread(&Runtime::run, this);
}
Runtime::~Runtime() {
    {
        std::lock_guard<std::mutex> lock(mutex_);
        stopping_ = true;
        for (auto &item : pending_) item.second->store(true);
    }
    wake_.notify_all();
    worker_.join();
}
Id Runtime::submit(Request request) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (stopping_ || pending_.size() >= capacity) return 0;
    request.id = next_id_++;
    if (request.operation == Operation::Load) request.model = request.id;
    auto cancelled = std::make_shared<std::atomic_bool>(false);
    const Id id = request.id;
    pending_[id] = cancelled;
    queue_.push_back({std::move(request), cancelled});
    wake_.notify_one();
    return id;
}
bool Runtime::cancel(Id id) {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto found = pending_.find(id);
    if (found == pending_.end()) return false;
    found->second->store(true);
    return true;
}
void Runtime::cancel_all() {
    std::lock_guard<std::mutex> lock(mutex_);
    for (auto &item : pending_) item.second->store(true);
}
std::vector<Event> Runtime::poll() {
    std::lock_guard<std::mutex> lock(mutex_);
    std::vector<Event> events;
    events.swap(events_);
    for (const auto &event : events) if (event.kind != Event::Kind::Token) pending_.erase(event.id);
    return events;
}
void Runtime::push(Event event) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (stopping_) return;
    // Coalesce adjacent chunks when the main thread is busy. Never block shutdown
    // on a consumer and never accumulate one allocation per generated token.
    if (event.kind == Event::Kind::Token && !events_.empty() &&
        events_.back().kind == event.kind && events_.back().id == event.id) {
        events_.back().result.text += event.result.text;
    } else events_.push_back(std::move(event));
}
void Runtime::run() {
    for (;;) {
        Work work;
        {
            std::unique_lock<std::mutex> lock(mutex_);
            wake_.wait(lock, [this] { return stopping_ || !queue_.empty(); });
            if (stopping_) {
                lock.unlock();
                backend_.reset(); // Free contexts on the same worker that used them.
                return;
            }
            work = std::move(queue_.front()); queue_.pop_front();
        }
        Event event;
        event.id = work.request.id; event.model = work.request.model; event.operation = work.request.operation;
        const auto start = std::chrono::steady_clock::now();
        try {
            if (work.cancelled->load()) throw std::runtime_error("Request cancelled");
            event.result = backend_->execute(work.request, *work.cancelled, [this, &work](const std::string &text) {
                Event chunk;
                chunk.kind = Event::Kind::Token; chunk.id = work.request.id;
                chunk.model = work.request.model; chunk.operation = work.request.operation;
                chunk.result.text = text; push(std::move(chunk));
            });
        } catch (const std::exception &error) {
            event.kind = Event::Kind::Failed; event.error = error.what();
        } catch (...) {
            event.kind = Event::Kind::Failed; event.error = "Unknown backend failure";
        }
        event.result.elapsed_ms = std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - start).count();
        push(std::move(event));
    }
}
} // namespace salmon
