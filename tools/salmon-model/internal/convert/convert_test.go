package convert

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/conversionenv"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/install"
)

func gitBlob(content []byte) string {
	hasher := sha1.New()
	_, _ = fmt.Fprintf(hasher, "blob %d\x00", len(content))
	_, _ = hasher.Write(content)
	return hex.EncodeToString(hasher.Sum(nil))
}
func gguf(label string) []byte {
	content := make([]byte, 128)
	copy(content, "GGUF")
	binary.LittleEndian.PutUint32(content[4:8], 3)
	copy(content[8:], label)
	return content
}

func sourceModel() (hub.Model, map[string][]byte) {
	contents := map[string][]byte{"config.json": []byte(`{"architectures":["LlamaForCausalLM"]}`), "tokenizer.json": []byte(`{"version":"1.0"}`), "model.safetensors": []byte("safe tensor weights")}
	weightHash := sha256.Sum256(contents["model.safetensors"])
	model := hub.Model{ID: "owner/source", SHA: "0123456789abcdef0123456789abcdef01234567", PipelineTag: "text-generation", Config: hub.ModelConfig{ModelType: "llama", Architectures: []string{"LlamaForCausalLM"}}, CardData: hub.CardData{License: "apache-2.0", LicenseLink: "https://example.test/license"}, Siblings: []hub.File{
		{Name: "tokenizer.json", Size: int64(len(contents["tokenizer.json"])), BlobID: gitBlob(contents["tokenizer.json"])},
		{Name: "model.safetensors", Size: int64(len(contents["model.safetensors"])), LFS: &hub.LFSInfo{SHA256: hex.EncodeToString(weightHash[:]), Size: int64(len(contents["model.safetensors"]))}},
		{Name: "config.json", Size: int64(len(contents["config.json"])), BlobID: gitBlob(contents["config.json"])},
	}}
	return model, contents
}
func environmentResolver() ResolveFunc {
	return func(context.Context, string) (conversionenv.Record, error) {
		return conversionenv.Record{ID: "converter-id", Version: "v1", PythonSHA256: strings.Repeat("1", 64), ConverterSHA256: strings.Repeat("2", 64), SitePackagesSHA256: strings.Repeat("3", 64), Python: "python", Converter: "converter", SitePackages: "packages"}, nil
	}
}
func downloadFrom(contents map[string][]byte) DownloadFunc {
	return func(ctx context.Context, _, _, filename string, expected, maximum int64, destination io.Writer, progress hub.ProgressFunc) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, ok := contents[filename]
		if !ok {
			return errors.New("unexpected source file")
		}
		if int64(len(data)) != expected || expected > maximum {
			return errors.New("bad bounds")
		}
		_, err := destination.Write(data)
		if progress != nil {
			progress(expected, expected)
		}
		return err
	}
}
func buildTestPlan(t *testing.T) (string, Plan, map[string][]byte, Dependencies) {
	t.Helper()
	root := t.TempDir()
	model, contents := sourceModel()
	deps := Dependencies{ResolveEnvironment: environmentResolver()}
	plan, err := BuildPlan(context.Background(), model, "f16", "converted-F16.gguf", root, deps)
	if err != nil {
		t.Fatal(err)
	}
	deps.Download = downloadFrom(contents)
	return root, plan, contents, deps
}

func TestConvertDownloadsVerifiesAndRegisters(t *testing.T) {
	root, plan, _, deps := buildTestPlan(t)
	events := 0
	deps.Run = func(_ context.Context, _ conversionenv.Record, source, output, outtype string, _, _ int64, progress ProgressFunc) error {
		if outtype != "f16" {
			return errors.New("wrong outtype")
		}
		if _, err := os.Stat(filepath.Join(source, "model.safetensors")); err != nil {
			return err
		}
		if progress != nil {
			progress("convert", "fake", 1, 1)
		}
		return os.WriteFile(output, gguf("converted"), 0o600)
	}
	record, err := Execute(context.Background(), nil, plan, plan.ConsentDigest, root, 1024, func(string, string, int64, int64) { events++ }, deps)
	if err != nil {
		t.Fatal(err)
	}
	if events == 0 || record.Origin != "converted" || record.PreparationPreset != "F16" || record.DerivedFromSHA256 != plan.SourceIdentitySHA256 || record.StructuralValidation != "gguf-header-passed-after-conversion" {
		t.Fatalf("unexpected record: %#v", record)
	}
	records, err := install.List(root)
	if err != nil || len(records) != 1 || records[0].Repository != "owner/source" {
		t.Fatalf("not in normal registry: %#v %v", records, err)
	}
	if _, err := install.Remove(root, record.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConsentEnvironmentAndSourceIdentityAreBound(t *testing.T) {
	root, plan, _, deps := buildTestPlan(t)
	deps.Run = func(context.Context, conversionenv.Record, string, string, string, int64, int64, ProgressFunc) error {
		return errors.New("must not run")
	}
	changed := plan
	changed.Outtype = "bf16"
	if _, err := Execute(context.Background(), nil, changed, plan.ConsentDigest, root, 1024, nil, deps); err == nil || !strings.Contains(err.Error(), "consent") {
		t.Fatalf("changed plan accepted: %v", err)
	}
	deps.ResolveEnvironment = func(context.Context, string) (conversionenv.Record, error) {
		record, _ := environmentResolver()(context.Background(), root)
		record.ConverterSHA256 = strings.Repeat("9", 64)
		return record, nil
	}
	if _, err := Execute(context.Background(), nil, plan, plan.ConsentDigest, root, 1024, nil, deps); err == nil || !strings.Contains(err.Error(), "environment changed") {
		t.Fatalf("changed environment accepted: %v", err)
	}
}

func TestBadDownloadCancellationAndInvalidOutputCleanUp(t *testing.T) {
	root, plan, contents, deps := buildTestPlan(t)
	bad := map[string][]byte{}
	for name, data := range contents {
		bad[name] = append([]byte(nil), data...)
	}
	bad["config.json"][0] ^= 1
	deps.Download = downloadFrom(bad)
	if _, err := Execute(context.Background(), nil, plan, plan.ConsentDigest, root, 1024, nil, deps); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("bad source accepted: %v", err)
	}
	deps.Download = downloadFrom(contents)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Execute(ctx, nil, plan, plan.ConsentDigest, root, 1024, nil, deps); err == nil {
		t.Fatal("cancelled conversion succeeded")
	}
	deps.Run = func(_ context.Context, _ conversionenv.Record, _, output, _ string, _, _ int64, _ ProgressFunc) error {
		return os.WriteFile(output, []byte("not gguf"), 0o600)
	}
	if _, err := Execute(context.Background(), nil, plan, plan.ConsentDigest, root, 1024, nil, deps); err == nil || !strings.Contains(err.Error(), "structural validation") {
		t.Fatalf("invalid output accepted: %v", err)
	}
	work, _ := filepath.Glob(filepath.Join(root, "downloads", "convert-*"))
	if len(work) != 0 {
		t.Fatalf("conversion work remains: %v", work)
	}
}

func TestSourceMutationDuringConversionDiscardsOutput(t *testing.T) {
	root, plan, contents, deps := buildTestPlan(t)
	deps.Run = func(_ context.Context, _ conversionenv.Record, source, output, _ string, _, _ int64, _ ProgressFunc) error {
		if err := os.WriteFile(output, gguf("converted"), 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(source, "config.json"), []byte("changed"), 0o600)
	}
	if _, err := Execute(context.Background(), nil, plan, plan.ConsentDigest, root, 1024, nil, deps); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("mutation accepted: %v", err)
	}
	records, _ := install.List(root)
	if len(records) != 0 {
		t.Fatalf("mutated conversion registered: %#v", records)
	}
	_ = contents
}

func TestRunRejectsUnhardenedConverterBeforeLaunching(t *testing.T) {
	converter := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(converter, []byte("trust_remote_code=True"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), conversionenv.Record{Converter: converter, Python: "not-a-python-binary"}, "source", "output", "f16", 1024, 2048, nil)
	if err == nil || !strings.Contains(err.Error(), "hardening patch") {
		t.Fatalf("unhardened converter accepted: %v", err)
	}
}

func TestPlanRejectsUnsupportedAndPyTorchOnlySources(t *testing.T) {
	root := t.TempDir()
	model, _ := sourceModel()
	model.Config.Architectures = []string{"UnknownForCausalLM"}
	if _, err := BuildPlan(context.Background(), model, "f16", "", root, Dependencies{ResolveEnvironment: environmentResolver()}); err == nil {
		t.Fatal("unsupported architecture accepted")
	}
	model, _ = sourceModel()
	model.Siblings[1].Name = "pytorch_model.bin"
	if _, err := BuildPlan(context.Background(), model, "f16", "", root, Dependencies{ResolveEnvironment: environmentResolver()}); err == nil {
		t.Fatal("PyTorch-only source accepted")
	}
}
