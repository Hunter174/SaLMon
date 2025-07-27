#!/bin/bash

# Get the directory where this script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

# Define URL and output path
MODEL_URL="https://huggingface.co/unsloth/SmolLM2-135M-Instruct-GGUF/resolve/main/SmolLM2-135M-Instruct-Q4_K_M.gguf?download=true"
OUTPUT="$SCRIPT_DIR/models/model.gguf"

# Ensure models directory exists
mkdir -p "$SCRIPT_DIR/models"

echo "Downloading model from $MODEL_URL ..."
curl -L "$MODEL_URL" -o "$OUTPUT"

# Check if curl succeeded
if [ $? -ne 0 ]; then
    echo "Download failed!"
    exit 1
fi

echo "Done! Model saved to $OUTPUT"