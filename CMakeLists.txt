cmake_minimum_required(VERSION 3.21)
project(salmon LANGUAGES C CXX)

if(CMAKE_SOURCE_DIR STREQUAL CMAKE_BINARY_DIR)
    message(FATAL_ERROR "Use an out-of-tree build: cmake -S . -B build/dev")
endif()
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
# godot-cpp defaults to a static MSVC CRT. Set it before creating llama.cpp and
# runtime targets so every library passed to the extension linker uses /MT.
# Consumers may still override this cache entry explicitly at configure time.
if(MSVC AND NOT CMAKE_MSVC_RUNTIME_LIBRARY)
    set(CMAKE_MSVC_RUNTIME_LIBRARY "MultiThreaded" CACHE STRING
        "MSVC runtime shared by SaLMoN, llama.cpp, and godot-cpp")
endif()

option(SALMON_BUILD_EXTENSION "Build the Godot extension" ON)
option(SALMON_VULKAN "Compile the Vulkan backend" OFF)
option(SALMON_CUDA "Compile the CUDA backend" OFF)
option(SALMON_METAL "Compile the Metal backend (Apple only)" OFF)
option(SALMON_NATIVE "Tune CPU instructions for this machine (not portable)" OFF)
option(SALMON_CPU_AVX2 "Require AVX2/FMA/F16C/BMI2 on x86 (faster, not universal)" OFF)
if(NOT SALMON_NATIVE)
    foreach(feature AVX AVX2 FMA F16C BMI2 SSE42)
        set(GGML_${feature} ${SALMON_CPU_AVX2} CACHE BOOL "" FORCE)
    endforeach()
endif()
set(BUILD_SHARED_LIBS OFF CACHE BOOL "Static runtime dependencies")
set(LLAMA_BUILD_TESTS OFF CACHE BOOL "" FORCE)
set(LLAMA_BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
set(LLAMA_BUILD_TOOLS OFF CACHE BOOL "" FORCE)
set(LLAMA_CURL OFF CACHE BOOL "" FORCE)
set(GGML_NATIVE ${SALMON_NATIVE} CACHE BOOL "" FORCE)
set(GGML_VULKAN ${SALMON_VULKAN} CACHE BOOL "" FORCE)
set(GGML_CUDA ${SALMON_CUDA} CACHE BOOL "" FORCE)
set(GGML_METAL ${SALMON_METAL} CACHE BOOL "" FORCE)
set(SALMON_LLAMA_SOURCE_DIR "${CMAKE_SOURCE_DIR}/lib/llama.cpp" CACHE PATH "llama.cpp source (override for clean dependency validation)")
add_subdirectory("${SALMON_LLAMA_SOURCE_DIR}" "${CMAKE_BINARY_DIR}/lib/llama.cpp" EXCLUDE_FROM_ALL)
if(MINGW AND TARGET ggml-cpu)
    # Older MinGW SDKs lack THREAD_POWER_THROTTLING_STATE used by this pin.
    # Disable only the optional newer thread APIs in this translation unit;
    # leave mmap/platform support and the user's submodule sources untouched.
    set_source_files_properties("${SALMON_LLAMA_SOURCE_DIR}/ggml/src/ggml-cpu/ggml-cpu.c"
        TARGET_DIRECTORY ggml-cpu PROPERTIES COMPILE_OPTIONS "-U_WIN32_WINNT;-D_WIN32_WINNT=0x0601")
endif()
find_package(Threads REQUIRED)

add_library(salmon_runtime STATIC src/runtime/runtime.cpp src/runtime/llama_backend.cpp)
target_include_directories(salmon_runtime PUBLIC src)
target_link_libraries(salmon_runtime PUBLIC llama Threads::Threads)
if(MINGW)
    # Avoid loading incompatible MinGW runtime DLLs from a player's PATH.
    target_link_options(salmon_runtime INTERFACE -static -static-libgcc -static-libstdc++)
endif()
# The pinned ggml's static MinGW target does not propagate OpenMP link flags.
if(GGML_OPENMP)
    find_package(OpenMP)
    if(OpenMP_CXX_FOUND)
        target_link_libraries(salmon_runtime PUBLIC OpenMP::OpenMP_CXX)
        if(MINGW)
            target_link_options(salmon_runtime INTERFACE -fopenmp)
        endif()
    endif()
endif()

if(SALMON_BUILD_EXTENSION)
    # Backend exceptions are caught at our worker/Godot boundary, never across the C ABI.
    set(GODOTCPP_DISABLE_EXCEPTIONS OFF CACHE BOOL "" FORCE)
    if(NOT GODOTCPP_BUILD_PROFILE)
        set(GODOTCPP_BUILD_PROFILE "${CMAKE_SOURCE_DIR}/cmake/godot-build-profile.json" CACHE FILEPATH "Godot API subset used by SaLMoN" FORCE)
    endif()
    add_subdirectory(lib/godot-cpp EXCLUDE_FROM_ALL)
    add_library(salmon SHARED src/entry.cpp src/register_types.cpp
        src/llm_node/llmnode.cpp src/salmon/salmon.cpp)
    target_link_libraries(salmon PRIVATE salmon_runtime godot::cpp)
    if(WIN32)
        set(SALMON_PLATFORM windows)
    elseif(APPLE)
        set(SALMON_PLATFORM macos)
    elseif(CMAKE_SYSTEM_NAME STREQUAL "Linux")
        set(SALMON_PLATFORM linux)
    else()
        message(FATAL_ERROR "This SaLMoN build currently targets desktop platforms only")
    endif()
    get_target_property(SALMON_ARCH godot-cpp GODOTCPP_ARCH)
    set_target_properties(salmon PROPERTIES PREFIX ""
        OUTPUT_NAME "salmon.${SALMON_PLATFORM}.${GODOTCPP_TARGET}.${SALMON_ARCH}"
        RUNTIME_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/addon/addons/salmon/bin"
        LIBRARY_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/addon/addons/salmon/bin")
    file(MAKE_DIRECTORY "${CMAKE_BINARY_DIR}/addon/addons/salmon")
    configure_file(addons/salmon/salmon.gdextension
        "${CMAKE_BINARY_DIR}/addon/addons/salmon/salmon.gdextension" COPYONLY)
    # Explicit convenience target: ordinary builds remain out-of-tree, while a
    # developer can stage the fresh extension into the checked-out Godot demo.
    add_custom_target(salmon_stage_godot_test
        DEPENDS salmon
        COMMAND ${CMAKE_COMMAND} -E make_directory "${CMAKE_SOURCE_DIR}/tests/godot/addons/salmon/bin"
        COMMAND ${CMAKE_COMMAND} -E copy_if_different "$<TARGET_FILE:salmon>"
            "${CMAKE_SOURCE_DIR}/tests/godot/addons/salmon/bin/$<TARGET_FILE_NAME:salmon>"
        COMMAND ${CMAKE_COMMAND} -E copy_if_different "${CMAKE_SOURCE_DIR}/addons/salmon/salmon.gdextension"
            "${CMAKE_SOURCE_DIR}/tests/godot/addons/salmon/salmon.gdextension"
        COMMENT "Staging SaLMoN into tests/godot/addons")
    install(TARGETS salmon RUNTIME DESTINATION addons/salmon/bin LIBRARY DESTINATION addons/salmon/bin)
    install(FILES addons/salmon/salmon.gdextension DESTINATION addons/salmon)
endif()

include(CTest)
if(BUILD_TESTING)
    add_executable(salmon_tests tests/runtime_tests.cpp)
    target_link_libraries(salmon_tests PRIVATE salmon_runtime)
    add_test(NAME salmon_runtime COMMAND salmon_tests)
    set_tests_properties(salmon_runtime PROPERTIES TIMEOUT 30)
    add_executable(salmon_model_smoke tests/model_smoke.cpp)
    target_link_libraries(salmon_model_smoke PRIVATE salmon_runtime)
    # Model weights are external, so this quality reporter is built but not
    # registered with CTest. Run it explicitly against candidate GGUF files.
    add_executable(salmon_semantic_benchmark tests/semantic_benchmark.cpp)
    target_link_libraries(salmon_semantic_benchmark PRIVATE salmon_runtime)
    add_executable(salmon_embedding_reference tests/embedding_reference.cpp)
    target_link_libraries(salmon_embedding_reference PRIVATE salmon_runtime)
    add_executable(salmon_performance_benchmark tests/performance_benchmark.cpp)
    target_include_directories(salmon_performance_benchmark PRIVATE "${SALMON_LLAMA_SOURCE_DIR}/vendor")
    target_link_libraries(salmon_performance_benchmark PRIVATE salmon_runtime)
endif()
