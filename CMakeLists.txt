# Here is how to run
# cmake -G "MinGW Makefiles" -DCMAKE_SH="CMAKE_SH-NOTFOUND" ..
# mingw32-make

cmake_minimum_required(VERSION 3.16)
project(salmon)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# 🧩 Static linking options for MinGW
if (MINGW AND WIN32)
    message(STATUS "Using MinGW on Windows — enabling static linking")
    set(STATIC_LINK_FLAGS "-static -static-libgcc -static-libstdc++")
    set(CMAKE_CXX_FLAGS "${CMAKE_CXX_FLAGS} ${STATIC_LINK_FLAGS}")
    set(CMAKE_EXE_LINKER_FLAGS "${CMAKE_EXE_LINKER_FLAGS} ${STATIC_LINK_FLAGS}")
    set(CMAKE_SHARED_LINKER_FLAGS "${CMAKE_SHARED_LINKER_FLAGS} ${STATIC_LINK_FLAGS}")
endif()

# 🐉 Add llama.cpp (statically)
add_subdirectory(lib/llama.cpp)

# 🧠 Godot headers
set(GODOT_CPP_PATH ${CMAKE_SOURCE_DIR}/lib/godot-cpp)
include_directories(
        ${GODOT_CPP_PATH}/include
        ${GODOT_CPP_PATH}/include/core
        ${GODOT_CPP_PATH}/include/gen
        ${GODOT_CPP_PATH}/gen/include
        ${GODOT_CPP_PATH}/gdextension
)

# 🛠️ Source files
add_library(salmon SHARED
        src/register_types.cpp
        src/llm_node/llmnode.cpp
        src/entry.cpp
)

# ✅ Fix Bad file descriptor by disabling mmap/mlock and enabling static build paths
target_compile_definitions(salmon PRIVATE
        LLAMA_NO_MMAP=1
        LLAMA_NO_MLOCK=1
        LLAMA_BUILD_STATIC
)

# 🔗 Link llama.cpp statically
target_link_libraries(salmon PRIVATE llama)

# 🎯 Link Godot static lib
target_link_directories(salmon PRIVATE ${GODOT_CPP_PATH}/bin)
target_link_libraries(salmon PRIVATE ${GODOT_CPP_PATH}/bin/libgodot-cpp.windows.template_debug.x86_64.a)

# 🧵 OpenMP (static)
find_package(OpenMP)
if (OpenMP_CXX_FOUND)
    target_compile_options(salmon PRIVATE ${OpenMP_CXX_FLAGS})
    target_link_libraries(salmon PRIVATE ${OpenMP_CXX_FLAGS})
endif()

# 🧼 Remove 'lib' prefix, add correct suffix
set_target_properties(salmon PROPERTIES
        PREFIX ""
        SUFFIX ".dll"
)

# 📦 Output naming
set(OS_NAME "windows")
set(ARCH "x86_64")
set_target_properties(salmon PROPERTIES
        OUTPUT_NAME "salmon.${OS_NAME}.template_debug.${ARCH}"
        RUNTIME_OUTPUT_DIRECTORY "${CMAKE_SOURCE_DIR}/addons/salmon/bin"
        LIBRARY_OUTPUT_DIRECTORY "${CMAKE_SOURCE_DIR}/addons/salmon/bin"
        ARCHIVE_OUTPUT_DIRECTORY "${CMAKE_SOURCE_DIR}/addons/salmon/bin"
)

# 💻 Windows-specific
if (WIN32)
    add_definitions(-D_CRT_SECURE_NO_WARNINGS)
endif()