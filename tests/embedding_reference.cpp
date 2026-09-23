#include "runtime/runtime.h"
#include <atomic>
#include <cmath>
#include <iomanip>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

namespace {
constexpr double reference[6][6] = {
    {1.0000002,  0.57349044,  0.03299103,  0.03836519,  0.02325577,  0.04073851},
    {0.57349044, 1.0000000,   0.07672853, -0.02463919, -0.07213959,  0.08057909},
    {0.03299103, 0.07672853,  0.9999999,   0.02264523, -0.00643058, -0.10362329},
    {0.03836519,-0.02463919,  0.02264523,  1.0000000,   0.52661824, -0.01984081},
    {0.02325577,-0.07213959, -0.00643058,  0.52661824,  0.99999994, -0.07716919},
    {0.04073851, 0.08057909, -0.10362329, -0.01984081, -0.07716919,  1.0000000}
};

float dot(const std::vector<float> &a, const std::vector<float> &b) {
    if (a.size() != b.size()) throw std::runtime_error("Embedding dimensions differ");
    double value = 0;
    for (size_t i = 0; i < a.size(); ++i) value += static_cast<double>(a[i]) * b[i];
    return static_cast<float>(value);
}
} // namespace

int main(int argc, char **argv) {
    if (argc != 2) {
        std::cerr << "Usage: salmon_embedding_reference <all-MiniLM-L6-v2.gguf>\n";
        return 2;
    }
    try {
        auto backend = salmon::make_llama_backend();
        std::atomic_bool cancelled{false};
        salmon::Request load;
        load.operation = salmon::Operation::Load;
        load.model = 1;
        load.path = argv[1];
        load.purpose = "embedding";
        load.settings.context = 512;
        load.settings.threads = 4;
        load.settings.threads_batch = 8;
        backend->execute(load, cancelled, {});

        salmon::Request request;
        request.operation = salmon::Operation::Embed;
        request.model = 1;
        request.texts = {
            "The cat sits on the mat.",
            "A feline is resting on a rug.",
            "The rocket launched into orbit.",
            "How do I treat a dangerous fever?",
            "Moonleaf tea reduces sickness and fever.",
            "The blacksmith sells iron swords."
        };
        const auto result = backend->execute(request, cancelled, {});
        if (result.embeddings.size() != request.texts.size()) throw std::runtime_error("Missing reference embeddings");

        double maximum_error = 0, total_error = 0;
        int comparisons = 0;
        std::cout << "GGUF cosine matrix:\n" << std::fixed << std::setprecision(6);
        for (size_t row = 0; row < result.embeddings.size(); ++row) {
            if (result.embeddings[row].size() != 384) throw std::runtime_error("Expected 384-dimensional MiniLM vectors");
            for (size_t column = 0; column < result.embeddings.size(); ++column) {
                const double similarity = dot(result.embeddings[row], result.embeddings[column]);
                const double error = std::abs(similarity - reference[row][column]);
                maximum_error = std::max(maximum_error, error);
                if (row < column) { total_error += error; ++comparisons; }
                std::cout << std::setw(10) << similarity;
            }
            std::cout << '\n';
        }
        std::cout << "Reference: sentence-transformers/all-MiniLM-L6-v2, normalized FP32 mean pooling\n"
                  << "Maximum cosine delta: " << maximum_error << '\n'
                  << "Mean off-diagonal cosine delta: " << total_error / comparisons << '\n';
        // Q4 quantization need not be coordinate-identical, but its semantic
        // geometry should remain close to the canonical model on fixed inputs.
        if (maximum_error > 0.08) throw std::runtime_error("GGUF embedding geometry exceeds 0.08 reference tolerance");
        std::cout << "Embedding reference agreement: PASSED\n";
        return 0;
    } catch (const std::exception &error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
