#include "register_types.h"
#include "llm_node/llmnode.h"

using namespace godot;

void initialize_salmon_module(ModuleInitializationLevel p_level) {
    if (p_level != MODULE_INITIALIZATION_LEVEL_SCENE) return;
    ClassDB::register_class<LLMNode>();
}

void uninitialize_salmon_module(ModuleInitializationLevel p_level) {
    if (p_level != MODULE_INITIALIZATION_LEVEL_SCENE) return;
    // Cleanup logic (if needed)
}
