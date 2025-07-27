@echo off
:: Get the directory of this .bat file
set SCRIPT_DIR=%~dp0

:: Remove trailing backslash if needed
set SCRIPT_DIR=%SCRIPT_DIR:~0,-1%

set MODEL_URL=https://huggingface.co/unsloth/SmolLM2-135M-Instruct-GGUF/resolve/main/SmolLM2-135M-Instruct-Q4_K_M.gguf?download=true
set OUTPUT=%SCRIPT_DIR%\models\model.gguf

:: Ensure models directory exists
if not exist "%SCRIPT_DIR%\models" mkdir "%SCRIPT_DIR%\models"

echo Downloading model from %MODEL_URL% ...
curl -L "%MODEL_URL%" -o "%OUTPUT%"

if %ERRORLEVEL% neq 0 (
    echo Download failed!
    pause
    exit /b 1
)

echo Done! Model saved to %OUTPUT%.