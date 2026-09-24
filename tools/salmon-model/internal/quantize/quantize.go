package quantize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/install"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/toolchain"
)

const DefaultMaximumOutputBytes int64 = 20 << 30

var (
	presets = map[string]struct{ minimum, maximum int64 }{
		"Q4_K_M": {15, 40},
		"Q5_K_M": {18, 50},
		"Q8_0":   {25, 70},
	}
	outputNamePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,239}\.gguf$`)
	tensorProgressPattern = regexp.MustCompile(`^\[\s*(\d+)\/\s*(\d+)\]`)
)

type Plan struct {
	SchemaVersion          int    `json:"schema_version"`
	Input                  string `json:"input"`
	InputSizeBytes         int64  `json:"input_size_bytes"`
	InputSHA256            string `json:"input_sha256"`
	Preset                 string `json:"preset"`
	OutputFilename         string `json:"output_filename"`
	OutputDirectoryPattern string `json:"output_directory_pattern"`
	EstimatedOutputMinimum int64  `json:"estimated_output_minimum_bytes"`
	EstimatedOutputMaximum int64  `json:"estimated_output_maximum_bytes"`
	EstimatedPeakDiskBytes int64  `json:"estimated_peak_disk_bytes"`
	ToolchainID            string `json:"toolchain_id"`
	ToolchainVersion       string `json:"toolchain_version"`
	ToolchainArchiveSHA256 string `json:"toolchain_archive_sha256"`
	ToolchainBinary        string `json:"toolchain_binary"`
	ToolchainBinarySHA256  string `json:"toolchain_binary_sha256"`
	ConsentDigest          string `json:"consent_digest,omitempty"`
	Warning                string `json:"warning"`
}

type ProgressFunc func(message string)
type ResolveFunc func(context.Context, string) (toolchain.Record, error)
type RunnerFunc func(context.Context, string, string, string, string, int64, ProgressFunc) error

type Dependencies struct {
	ResolveToolchain ResolveFunc
	Run              RunnerFunc
}

func BuildPlan(ctx context.Context, input, preset, outputName, root string, dependencies Dependencies) (Plan, error) {
	preset = strings.ToUpper(strings.TrimSpace(preset))
	rangeValue, ok := presets[preset]
	if !ok {
		return Plan{}, errors.New("preset must be one of Q4_K_M, Q5_K_M, or Q8_0")
	}
	absoluteInput, err := filepath.Abs(input)
	if err != nil {
		return Plan{}, err
	}
	info, err := os.Lstat(absoluteInput)
	if err != nil {
		return Plan{}, err
	}
	if !info.Mode().IsRegular() {
		return Plan{}, errors.New("quantization input must be a regular file, not a symlink or special file")
	}
	if err := install.ValidateGGUF(absoluteInput); err != nil {
		return Plan{}, fmt.Errorf("quantization input is not a structurally valid GGUF: %w", err)
	}
	inputHash, err := install.HashFile(absoluteInput)
	if err != nil {
		return Plan{}, err
	}
	if outputName == "" {
		base := strings.TrimSuffix(filepath.Base(absoluteInput), filepath.Ext(absoluteInput))
		outputName = base + "-" + preset + ".gguf"
	}
	if !outputNamePattern.MatchString(outputName) || filepath.Base(outputName) != outputName {
		return Plan{}, errors.New("output name must be a safe .gguf filename")
	}
	if dependencies.ResolveToolchain == nil {
		dependencies.ResolveToolchain = toolchain.Resolve
	}
	quantizer, err := dependencies.ResolveToolchain(ctx, root)
	if err != nil {
		return Plan{}, err
	}
	root, err = resolvedRoot(root)
	if err != nil {
		return Plan{}, err
	}
	minimum := info.Size() * rangeValue.minimum / 100
	maximum := info.Size() * rangeValue.maximum / 100
	plan := Plan{
		SchemaVersion: 1, Input: absoluteInput, InputSizeBytes: info.Size(), InputSHA256: inputHash,
		Preset: preset, OutputFilename: outputName,
		OutputDirectoryPattern: filepath.Join(root, "models", "sha256", "<generated-sha256>"),
		EstimatedOutputMinimum: minimum, EstimatedOutputMaximum: maximum,
		EstimatedPeakDiskBytes: info.Size() + maximum,
		ToolchainID:            quantizer.ID, ToolchainVersion: quantizer.Version,
		ToolchainArchiveSHA256: quantizer.SHA256, ToolchainBinary: quantizer.Binary,
		ToolchainBinarySHA256: quantizer.BinarySHA256,
		Warning:               "Size ranges are rough because GGUF tensor types and architecture affect output. Requantization is intentionally not enabled. Installing a toolchain did not authorize this execution.",
	}
	plan.ConsentDigest, err = Digest(plan)
	return plan, err
}

func Digest(plan Plan) (string, error) {
	plan.ConsentDigest = ""
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func Execute(ctx context.Context, plan Plan, consent, root string, maximumOutputBytes int64, progress ProgressFunc, dependencies Dependencies) (install.Record, error) {
	digest, err := Digest(plan)
	if err != nil {
		return install.Record{}, err
	}
	if consent == "" || !strings.EqualFold(consent, digest) || !strings.EqualFold(plan.ConsentDigest, digest) {
		return install.Record{}, errors.New("consent digest does not match the exact quantization plan")
	}
	if maximumOutputBytes <= 0 {
		maximumOutputBytes = DefaultMaximumOutputBytes
	}
	if dependencies.ResolveToolchain == nil {
		dependencies.ResolveToolchain = toolchain.Resolve
	}
	if dependencies.Run == nil {
		dependencies.Run = Run
	}
	root, err = resolvedRoot(root)
	if err != nil {
		return install.Record{}, err
	}
	if filepath.Clean(plan.OutputDirectoryPattern) != filepath.Join(root, "models", "sha256", "<generated-sha256>") {
		return install.Record{}, errors.New("quantization output destination differs from the consented managed-storage pattern")
	}
	if _, ok := presets[plan.Preset]; !ok || !outputNamePattern.MatchString(plan.OutputFilename) || filepath.Base(plan.OutputFilename) != plan.OutputFilename {
		return install.Record{}, errors.New("quantization plan contains an unsupported preset or unsafe output name")
	}
	absoluteInput, err := filepath.Abs(plan.Input)
	if err != nil || filepath.Clean(absoluteInput) != filepath.Clean(plan.Input) {
		return install.Record{}, errors.New("quantization input path differs from the consented plan")
	}
	info, err := os.Lstat(plan.Input)
	if err != nil {
		return install.Record{}, err
	}
	if !info.Mode().IsRegular() || info.Size() != plan.InputSizeBytes {
		return install.Record{}, errors.New("quantization input type or size changed after consent")
	}
	if err := install.ValidateGGUF(plan.Input); err != nil {
		return install.Record{}, err
	}
	inputHash, err := install.HashFile(plan.Input)
	if err != nil {
		return install.Record{}, err
	}
	if !strings.EqualFold(inputHash, plan.InputSHA256) {
		return install.Record{}, errors.New("quantization input SHA-256 changed after consent")
	}
	quantizer, err := dependencies.ResolveToolchain(ctx, root)
	if err != nil {
		return install.Record{}, err
	}
	if quantizer.ID != plan.ToolchainID || quantizer.Version != plan.ToolchainVersion || !strings.EqualFold(quantizer.SHA256, plan.ToolchainArchiveSHA256) || filepath.Clean(quantizer.Binary) != filepath.Clean(plan.ToolchainBinary) || !strings.EqualFold(quantizer.BinarySHA256, plan.ToolchainBinarySHA256) {
		return install.Record{}, errors.New("installed quantizer identity changed after consent")
	}
	downloadDirectory := filepath.Join(root, "downloads")
	if err := os.MkdirAll(downloadDirectory, 0o755); err != nil {
		return install.Record{}, err
	}
	temporary := filepath.Join(downloadDirectory, "quantize-"+digest+".part.gguf")
	_ = os.Remove(temporary)
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if progress != nil {
		progress("Starting pinned llama-quantize " + plan.ToolchainVersion + " with preset " + plan.Preset)
	}
	if err := dependencies.Run(ctx, quantizer.Binary, plan.Input, temporary, plan.Preset, maximumOutputBytes, progress); err != nil {
		return install.Record{}, fmt.Errorf("quantization failed: %w", err)
	}
	outputInfo, err := os.Stat(temporary)
	if err != nil {
		return install.Record{}, errors.New("quantizer completed without producing the planned output")
	}
	if !outputInfo.Mode().IsRegular() || outputInfo.Size() <= 0 || outputInfo.Size() > maximumOutputBytes {
		return install.Record{}, errors.New("quantized output has an invalid type or exceeds the configured maximum size")
	}
	if err := install.ValidateGGUF(temporary); err != nil {
		return install.Record{}, fmt.Errorf("quantized output failed GGUF structural validation: %w", err)
	}
	outputHash, err := install.HashFile(temporary)
	if err != nil {
		return install.Record{}, err
	}
	inputHashAfter, err := install.HashFile(plan.Input)
	if err != nil {
		return install.Record{}, err
	}
	if !strings.EqualFold(inputHashAfter, plan.InputSHA256) {
		return install.Record{}, errors.New("quantization input changed while the tool was running; output was discarded")
	}
	destination := filepath.Join(root, "models", "sha256", outputHash, plan.OutputFilename)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return install.Record{}, err
	}
	created := false
	if existing, err := os.Stat(destination); err == nil {
		if existing.Size() != outputInfo.Size() {
			return install.Record{}, errors.New("existing content-addressed output has the wrong size")
		}
		existingHash, err := install.HashFile(destination)
		if err != nil || !strings.EqualFold(existingHash, outputHash) {
			return install.Record{}, errors.New("existing content-addressed output is corrupt")
		}
		_ = os.Remove(temporary)
	} else if !os.IsNotExist(err) {
		return install.Record{}, err
	} else if err := os.Rename(temporary, destination); err != nil {
		return install.Record{}, fmt.Errorf("atomically promote quantized model: %w", err)
	} else {
		created = true
	}
	removeTemporary = false
	source, err := install.FindBySHA256(root, plan.InputSHA256)
	if err != nil {
		if created {
			_ = os.Remove(destination)
		}
		return install.Record{}, err
	}
	record, err := install.RegisterGenerated(root, destination, install.GeneratedMetadata{
		Filename: plan.OutputFilename, SHA256: outputHash, SizeBytes: outputInfo.Size(),
		DerivedFromSHA256: plan.InputSHA256, PreparationPreset: plan.Preset,
		PreparationToolchain: plan.ToolchainID, Source: source,
	})
	if err != nil {
		if created {
			_ = os.Remove(destination)
		}
		return install.Record{}, err
	}
	if progress != nil {
		progress("Quantized output passed GGUF structural validation and was registered")
	}
	return record, nil
}

func Run(ctx context.Context, binary, input, output, preset string, maximumOutputBytes int64, progress ProgressFunc) error {
	processContext, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(processContext, binary, input, output, preset)
	directory := filepath.Dir(binary)
	command.Env = append(os.Environ(), "LD_LIBRARY_PATH="+directory, "DYLD_LIBRARY_PATH="+directory)
	writer := &progressWriter{callback: progress}
	command.Stdout, command.Stderr = writer, writer
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return fmt.Errorf("llama-quantize exited with %v%s", err, writer.summary())
			}
			return nil
		case <-ctx.Done():
			cancel()
			<-done
			return ctx.Err()
		case <-ticker.C:
			if info, err := os.Stat(output); err == nil && info.Size() > maximumOutputBytes {
				cancel()
				<-done
				return errors.New("quantized output exceeded the configured maximum size")
			}
		}
	}
}

func resolvedRoot(root string) (string, error) {
	if root == "" {
		var err error
		root, err = install.DefaultRoot()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(root)
}

type progressWriter struct {
	mutex      sync.Mutex
	callback   ProgressFunc
	lastBucket int
	tail       []string
}

func (writer *progressWriter) Write(data []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 4096 {
			line = line[:4096] + "…"
		}
		writer.tail = append(writer.tail, line)
		if len(writer.tail) > 8 {
			writer.tail = writer.tail[len(writer.tail)-8:]
		}
		if writer.callback == nil {
			continue
		}
		if match := tensorProgressPattern.FindStringSubmatch(line); len(match) == 3 {
			var current, total int
			_, _ = fmt.Sscanf(match[1], "%d", &current)
			_, _ = fmt.Sscanf(match[2], "%d", &total)
			if total > 0 {
				bucket := current * 20 / total
				if bucket > writer.lastBucket || current == total {
					writer.lastBucket = bucket
					writer.callback(fmt.Sprintf("Quantizing tensors: %d/%d (%d%%)", current, total, current*100/total))
				}
			}
			continue
		}
		if strings.Contains(line, "model size") || strings.Contains(line, "quant size") || strings.Contains(line, "WARNING:") || strings.Contains(line, "quantize time") || strings.Contains(line, "total time") {
			writer.callback(line)
		}
	}
	return len(data), nil
}

func (writer *progressWriter) summary() string {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if len(writer.tail) == 0 {
		return ""
	}
	return "; last output: " + strings.Join(writer.tail, " | ")
}

var _ io.Writer = (*progressWriter)(nil)
