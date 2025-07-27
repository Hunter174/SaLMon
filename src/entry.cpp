#ifdef _WIN32
#define SALMON_EXPORT __declspec(dllexport)
#else
#define SALMON_EXPORT
#endif

#include <godot_cpp/godot.hpp>
#include "register_types.h"

extern "C" {
    GDExtensionBool GDE_EXPORT salmon_library_init(
            GDExtensionInterfaceGetProcAddress p_get_proc_address,
            GDExtensionClassLibraryPtr p_library,
            GDExtensionInitialization* r_initialization
    ) {
        godot::GDExtensionBinding::InitObject init_obj(p_get_proc_address, p_library, r_initialization);
        init_obj.register_initializer(initialize_salmon_module);
        init_obj.register_terminator(uninitialize_salmon_module);
        init_obj.set_minimum_library_initialization_level(godot::MODULE_INITIALIZATION_LEVEL_SCENE);
        return init_obj.init();
    }
}
