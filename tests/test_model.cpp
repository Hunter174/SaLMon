include "llama.h"
#include <iostream>

int main() {
    const char* model_path = "C:/Users/7568316/Desktop/repos/SaLMon/src/models\SmolLM2-135M-Instruct-Q4_K_M.gguf";

    std::cout << "Loading model: " << model_path << std::endl;

    llama_model_params model_params = llama_model_default_params();
    llama_model* model = llama_model_load_from_file(model_path, model_params);

    if (!model) {
        std::cerr << "❌ Failed to load model!" << std::endl;
        perror("C error (fopen)");
        return 1;
    }

    std::cout << "✅ Model loaded successfully!" << std::endl;
    llama_model_free(model);
    return 0;
}
