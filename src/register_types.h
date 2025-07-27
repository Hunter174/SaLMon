#ifndef SALMON_REGISTER_TYPES_H
#define SALMON_REGISTER_TYPES_H

#include <godot_cpp/core/class_db.hpp>
#include <godot_cpp/core/defs.hpp>
#include <gdextension_interface.h>
#include <godot_cpp/godot.hpp>

using namespace godot;

void initialize_salmon_module(ModuleInitializationLevel p_level);
void uninitialize_salmon_module(ModuleInitializationLevel p_level);

#endif // SALMON_REGISTER_TYPES_H