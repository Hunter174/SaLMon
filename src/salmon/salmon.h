#pragma once
#include <godot_cpp/classes/node.hpp>
#include <godot_cpp/variant/array.hpp>
#include <godot_cpp/variant/dictionary.hpp>
#include <godot_cpp/variant/packed_string_array.hpp>
#include "runtime/runtime.h"

// Godot-facing adapter. Call methods from the main thread. Runtime owns all work.
class Salmon : public godot::Node {
    GDCLASS(Salmon, godot::Node);
    // Editor introspection creates temporary nodes; do not start workers until needed.
    std::unique_ptr<salmon::Runtime> runtime_;
protected:
    static void _bind_methods();
    virtual void on_event(const salmon::Event &) {}
    salmon::Id submit(salmon::Request request);
public:
    Salmon();
    ~Salmon() override = default;
    void _process(double delta) override;
    int64_t load_model(const godot::String &path, const godot::String &purpose, const godot::Dictionary &settings = {});
    int64_t unload_model(int64_t model);
    int64_t chat(int64_t model, const godot::Array &messages, int max_tokens = 128, bool stream = false);
    int64_t decide(int64_t model, const godot::String &state, const godot::String &question, const godot::Array &options);
    int64_t embed(int64_t model, const godot::PackedStringArray &texts);
    bool cancel(int64_t request_id);
    void cancel_all();
};
