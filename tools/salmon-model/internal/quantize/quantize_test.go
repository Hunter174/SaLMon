package quantize

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/install"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/toolchain"
)

func validGGUF(label string) []byte {
	content := make([]byte, 128)
	copy(content, "GGUF")
	binary.LittleEndian.PutUint32(content[4:8], 3)
	copy(content[8:], label)
	return content
}

func testResolver(binaryPath string) ResolveFunc {
	hash := sha256.Sum256([]byte("fake binary"))
	return func(context.Context, string) (toolchain.Record, error) {
		return toolchain.Record{
			ID: "llama-quantize-b6002-test-amd64", ToolchainID: toolchain.ToolchainID,
			Version: toolchain.Version, SHA256: strings.Repeat("a", 64), Binary: binaryPath,
			BinarySHA256: hex.EncodeToString(hash[:]),
		}, nil
	}
}

func setupPlan(t *testing.T) (string, string, Plan, Dependencies) {
	t.Helper()
	root := t.TempDir()
	input := filepath.Join(root, "source-f16.gguf")
	if err := os.WriteFile(input, validGGUF("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(root, "fake-quantizer")
	if err := os.WriteFile(binaryPath, []byte("fake binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	dependencies := Dependencies{ResolveToolchain: testResolver(binaryPath)}
	plan, err := BuildPlan(context.Background(), input, "Q4_K_M", "", root, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	return root, input, plan, dependencies
}

func TestQuantizeRegistersValidatedManagedOutput(t *testing.T) {
	root, _, plan, dependencies := setupPlan(t)
	progressCount := 0
	dependencies.Run = func(_ context.Context, _, _, output, preset string, _ int64, progress ProgressFunc) error {
		if preset != "Q4_K_M" {
			return errors.New("wrong preset")
		}
		if progress != nil {
			progress("fake progress")
		}
		return os.WriteFile(output, validGGUF("quantized"), 0o600)
	}
	record, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1024, func(string) { progressCount++ }, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if record.Origin != "quantized" || record.PreparationPreset != "Q4_K_M" || record.DerivedFromSHA256 != plan.InputSHA256 || record.RuntimeValidation != "not-run" {
		t.Fatalf("unexpected generated record: %#v", record)
	}
	if progressCount < 2 {
		t.Fatalf("expected bounded progress events, got %d", progressCount)
	}
	if err := install.ValidateGGUF(record.Path); err != nil {
		t.Fatal(err)
	}
	records, err := install.List(root)
	if err != nil || len(records) != 1 || records[0].ID != record.ID {
		t.Fatalf("generated model did not enter normal registry: %#v, %v", records, err)
	}
	if _, err := install.Remove(root, record.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConsentAndInputIdentityAreExact(t *testing.T) {
	root, input, plan, dependencies := setupPlan(t)
	dependencies.Run = func(context.Context, string, string, string, string, int64, ProgressFunc) error {
		return errors.New("runner must not start")
	}
	changed := plan
	changed.Preset = "Q5_K_M"
	if _, err := Execute(context.Background(), changed, plan.ConsentDigest, root, 1024, nil, dependencies); err == nil || !strings.Contains(err.Error(), "consent") {
		t.Fatalf("modified plan was not rejected by consent: %v", err)
	}
	if err := os.WriteFile(input, validGGUF("changed input"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1024, nil, dependencies); err == nil || !strings.Contains(err.Error(), "SHA-256 changed") {
		t.Fatalf("changed input was not rejected: %v", err)
	}
}

func TestCancellationAndInvalidOutputAreCleaned(t *testing.T) {
	root, _, plan, dependencies := setupPlan(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dependencies.Run = func(ctx context.Context, _, _, output, _ string, _ int64, _ ProgressFunc) error {
		_ = os.WriteFile(output, []byte("partial"), 0o600)
		return ctx.Err()
	}
	if _, err := Execute(ctx, plan, plan.ConsentDigest, root, 1024, nil, dependencies); err == nil {
		t.Fatal("cancelled quantization succeeded")
	}
	parts, _ := filepath.Glob(filepath.Join(root, "downloads", "quantize-*.part.gguf"))
	if len(parts) != 0 {
		t.Fatalf("cancelled output remains: %v", parts)
	}
	dependencies.Run = func(_ context.Context, _, _, output, _ string, _ int64, _ ProgressFunc) error {
		return os.WriteFile(output, []byte("not gguf"), 0o600)
	}
	if _, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1024, nil, dependencies); err == nil || !strings.Contains(err.Error(), "structural validation") {
		t.Fatalf("invalid output was not rejected: %v", err)
	}
}

func TestInputMutationDuringExecutionDiscardsOutput(t *testing.T) {
	root, input, plan, dependencies := setupPlan(t)
	dependencies.Run = func(_ context.Context, _, _, output, _ string, _ int64, _ ProgressFunc) error {
		if err := os.WriteFile(output, validGGUF("quantized"), 0o600); err != nil {
			return err
		}
		return os.WriteFile(input, validGGUF("mutated while running"), 0o600)
	}
	if _, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1024, nil, dependencies); err == nil || !strings.Contains(err.Error(), "while the tool was running") {
		t.Fatalf("mid-run mutation was not rejected: %v", err)
	}
	records, _ := install.List(root)
	if len(records) != 0 {
		t.Fatalf("mutated operation registered output: %#v", records)
	}
}

func TestProgressWriterThrottlesVerboseQuantizerOutput(t *testing.T) {
	messages := []string{}
	writer := &progressWriter{callback: func(message string) { messages = append(messages, message) }}
	_, _ = writer.Write([]byte("metadata noise\n[   1/ 100] tensor\n[   5/ 100] tensor\n[   6/ 100] tensor\nllama_model_quantize_impl: quant size = 10 MB\n"))
	if len(messages) != 2 || !strings.Contains(messages[0], "5/100") || !strings.Contains(messages[1], "quant size") {
		t.Fatalf("unexpected throttled progress: %#v", messages)
	}
	if !strings.Contains(writer.summary(), "metadata noise") {
		t.Fatalf("error summary did not retain bounded diagnostic tail: %s", writer.summary())
	}
}

func TestPlanRejectsUnsafeInputsAndPresets(t *testing.T) {
	root, input, _, dependencies := setupPlan(t)
	if _, err := BuildPlan(context.Background(), input, "IQ1_M", "", root, dependencies); err == nil {
		t.Fatal("unsupported preset accepted")
	}
	if _, err := BuildPlan(context.Background(), input, "Q4_K_M", "../escape.gguf", root, dependencies); err == nil {
		t.Fatal("unsafe output name accepted")
	}
}
