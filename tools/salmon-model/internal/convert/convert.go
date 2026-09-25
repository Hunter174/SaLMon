package convert

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/compat"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/conversionenv"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/install"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/planner"
)

const DefaultMaximumSourceBytes int64 = 20 << 30

var outputNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,239}\.gguf$`)

type SourceFile struct {
	Name                  string `json:"name"`
	Role                  string `json:"role"`
	SizeBytes             int64  `json:"size_bytes"`
	VerificationAlgorithm string `json:"verification_algorithm"`
	VerificationDigest    string `json:"verification_digest"`
}

type Plan struct {
	SchemaVersion                 int          `json:"schema_version"`
	Repository                    string       `json:"repository"`
	ResolvedSHA                   string       `json:"resolved_sha"`
	Architecture                  string       `json:"architecture"`
	SourceFormat                  string       `json:"source_format"`
	SourceFiles                   []SourceFile `json:"source_files"`
	SourceBytes                   int64        `json:"source_bytes"`
	SourceIdentitySHA256          string       `json:"source_identity_sha256"`
	Outtype                       string       `json:"outtype"`
	OutputFilename                string       `json:"output_filename"`
	EstimatedOutputMinimum        int64        `json:"estimated_output_minimum_bytes"`
	MaximumOutputBytes            int64        `json:"maximum_output_bytes"`
	MaximumWorkingBytes           int64        `json:"maximum_working_bytes"`
	OutputDirectoryPattern        string       `json:"output_directory_pattern"`
	License                       string       `json:"license"`
	LicenseURL                    string       `json:"license_url,omitempty"`
	BaseModel                     any          `json:"base_model,omitempty"`
	SourceURL                     string       `json:"source_url"`
	EnvironmentID                 string       `json:"environment_id"`
	EnvironmentVersion            string       `json:"environment_version"`
	EnvironmentPythonSHA256       string       `json:"environment_python_sha256"`
	EnvironmentConverterSHA256    string       `json:"environment_converter_sha256"`
	EnvironmentDependenciesSHA256 string       `json:"environment_dependencies_sha256"`
	ConsentDigest                 string       `json:"consent_digest,omitempty"`
	Warning                       string       `json:"warning"`
}

type ProgressFunc func(stage, message string, completed, total int64)
type ResolveFunc func(context.Context, string) (conversionenv.Record, error)
type DownloadFunc func(context.Context, string, string, string, int64, int64, io.Writer, hub.ProgressFunc) error
type RunnerFunc func(context.Context, conversionenv.Record, string, string, string, int64, int64, ProgressFunc) error

type Dependencies struct {
	ResolveEnvironment ResolveFunc
	Download           DownloadFunc
	Run                RunnerFunc
}

func BuildPlan(ctx context.Context, model hub.Model, outtype, outputName, root string, dependencies Dependencies) (Plan, error) {
	outtype = strings.ToLower(strings.TrimSpace(outtype))
	if outtype != "f16" && outtype != "bf16" {
		return Plan{}, errors.New("outtype must be f16 or bf16")
	}
	assessment := compat.Assess(model)
	if assessment.Status != "conversion_toolchain_required" {
		return Plan{}, fmt.Errorf("repository is not a complete supported conversion source: %s: %s", assessment.Status, assessment.Explanation)
	}
	if assessment.SourceFormat != "safetensors" {
		return Plan{}, errors.New("only Safetensors source conversion is enabled; PyTorch pickle sources remain disabled")
	}
	files, weightBytes, err := sourceFiles(model)
	if err != nil {
		return Plan{}, err
	}
	if outputName == "" {
		outputName = safeRepositoryName(model.ID) + "-" + strings.ToUpper(outtype) + ".gguf"
	}
	if !outputNamePattern.MatchString(outputName) || filepath.Base(outputName) != outputName {
		return Plan{}, errors.New("output name must be a safe .gguf filename")
	}
	if dependencies.ResolveEnvironment == nil {
		dependencies.ResolveEnvironment = conversionenv.Resolve
	}
	environment, err := dependencies.ResolveEnvironment(ctx, root)
	if err != nil {
		return Plan{}, err
	}
	root, err = resolvedRoot(root)
	if err != nil {
		return Plan{}, err
	}
	modelPlan := planner.Build(model)
	minimum := weightBytes * 8 / 10
	maximum := weightBytes*13/10 + 64<<20
	plan := Plan{SchemaVersion: 1, Repository: model.ID, ResolvedSHA: model.SHA, Architecture: modelPlan.Architecture, SourceFormat: "safetensors", SourceFiles: files, SourceBytes: sumSizes(files), Outtype: outtype, OutputFilename: outputName, EstimatedOutputMinimum: minimum, MaximumOutputBytes: maximum, MaximumWorkingBytes: sumSizes(files) + maximum + 256<<20, OutputDirectoryPattern: filepath.Join(root, "models", "sha256", "<generated-sha256>"), License: modelPlan.License, LicenseURL: model.CardData.LicenseLink, BaseModel: model.CardData.BaseModel, SourceURL: "https://huggingface.co/" + model.ID + "/tree/" + model.SHA, EnvironmentID: environment.ID, EnvironmentVersion: environment.Version, EnvironmentPythonSHA256: environment.PythonSHA256, EnvironmentConverterSHA256: environment.ConverterSHA256, EnvironmentDependenciesSHA256: environment.SitePackagesSHA256, Warning: "Conversion uses only the selected immutable Safetensors/config/tokenizer files. Repository Python code is neither downloaded nor executed. Output validation is structural; runtime inference remains not-run."}
	plan.SourceIdentitySHA256 = sourceIdentity(plan)
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

func Execute(ctx context.Context, client *hub.Client, plan Plan, consent, root string, maximumSourceBytes int64, progress ProgressFunc, dependencies Dependencies) (install.Record, error) {
	digest, err := Digest(plan)
	if err != nil {
		return install.Record{}, err
	}
	if consent == "" || !strings.EqualFold(consent, digest) || !strings.EqualFold(plan.ConsentDigest, digest) {
		return install.Record{}, errors.New("consent digest does not match the exact conversion plan")
	}
	if maximumSourceBytes <= 0 {
		maximumSourceBytes = DefaultMaximumSourceBytes
	}
	if plan.SourceBytes <= 0 || plan.SourceBytes > maximumSourceBytes {
		return install.Record{}, errors.New("planned conversion source exceeds the configured size limit")
	}
	if plan.Outtype != "f16" && plan.Outtype != "bf16" {
		return install.Record{}, errors.New("conversion plan contains an unsupported output type")
	}
	if !outputNamePattern.MatchString(plan.OutputFilename) || filepath.Base(plan.OutputFilename) != plan.OutputFilename {
		return install.Record{}, errors.New("conversion plan contains an unsafe output name")
	}
	root, err = resolvedRoot(root)
	if err != nil {
		return install.Record{}, err
	}
	if filepath.Clean(plan.OutputDirectoryPattern) != filepath.Join(root, "models", "sha256", "<generated-sha256>") {
		return install.Record{}, errors.New("conversion output destination differs from the consented managed-storage pattern")
	}
	if plan.SourceIdentitySHA256 != sourceIdentity(plan) {
		return install.Record{}, errors.New("conversion source identity does not match the consented file list")
	}
	if dependencies.ResolveEnvironment == nil {
		dependencies.ResolveEnvironment = conversionenv.Resolve
	}
	environment, err := dependencies.ResolveEnvironment(ctx, root)
	if err != nil {
		return install.Record{}, err
	}
	if environment.ID != plan.EnvironmentID || environment.Version != plan.EnvironmentVersion || !strings.EqualFold(environment.PythonSHA256, plan.EnvironmentPythonSHA256) || !strings.EqualFold(environment.ConverterSHA256, plan.EnvironmentConverterSHA256) || !strings.EqualFold(environment.SitePackagesSHA256, plan.EnvironmentDependenciesSHA256) {
		return install.Record{}, errors.New("installed conversion environment changed after consent")
	}
	if dependencies.Download == nil {
		if client == nil {
			return install.Record{}, errors.New("Hugging Face client is required")
		}
		dependencies.Download = client.DownloadFile
	}
	if dependencies.Run == nil {
		dependencies.Run = Run
	}
	if err := os.MkdirAll(filepath.Join(root, "downloads"), 0o700); err != nil {
		return install.Record{}, err
	}
	work, err := os.MkdirTemp(filepath.Join(root, "downloads"), "convert-"+digest[:12]+"-")
	if err != nil {
		return install.Record{}, err
	}
	defer os.RemoveAll(work)
	if err := os.MkdirAll(filepath.Join(work, "source"), 0o700); err != nil {
		return install.Record{}, err
	}
	var completed int64
	for _, file := range plan.SourceFiles {
		target, err := safeSourcePath(filepath.Join(work, "source"), file.Name)
		if err != nil {
			return install.Record{}, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return install.Record{}, err
		}
		if progress != nil {
			progress("download", file.Name, completed, plan.SourceBytes)
		}
		if err := downloadAndVerify(ctx, dependencies.Download, plan.Repository, plan.ResolvedSHA, file, target, maximumSourceBytes-completed, func(done, total int64) {
			if progress != nil {
				progress("download", file.Name, completed+done, plan.SourceBytes)
			}
		}); err != nil {
			return install.Record{}, err
		}
		completed += file.SizeBytes
	}
	output := filepath.Join(work, "output.part.gguf")
	if err := dependencies.Run(ctx, environment, filepath.Join(work, "source"), output, plan.Outtype, plan.MaximumOutputBytes, plan.MaximumWorkingBytes, progress); err != nil {
		return install.Record{}, fmt.Errorf("conversion failed: %w", err)
	}
	info, err := os.Stat(output)
	if err != nil {
		return install.Record{}, errors.New("converter completed without producing the planned output")
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > plan.MaximumOutputBytes {
		return install.Record{}, errors.New("converted output is invalid or exceeds the consented limit")
	}
	if err := install.ValidateGGUF(output); err != nil {
		return install.Record{}, fmt.Errorf("converted output failed GGUF structural validation: %w", err)
	}
	for _, file := range plan.SourceFiles {
		target, _ := safeSourcePath(filepath.Join(work, "source"), file.Name)
		if err := verifyFile(target, file); err != nil {
			return install.Record{}, errors.New("conversion source changed while the converter was running; output was discarded")
		}
	}
	outputHash, err := install.HashFile(output)
	if err != nil {
		return install.Record{}, err
	}
	destination := filepath.Join(root, "models", "sha256", outputHash, plan.OutputFilename)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return install.Record{}, err
	}
	created := false
	if existing, err := os.Stat(destination); err == nil {
		if existing.Size() != info.Size() {
			return install.Record{}, errors.New("existing content-addressed converted model has the wrong size")
		}
		hash, err := install.HashFile(destination)
		if err != nil || !strings.EqualFold(hash, outputHash) {
			return install.Record{}, errors.New("existing content-addressed converted model is corrupt")
		}
	} else if !os.IsNotExist(err) {
		return install.Record{}, err
	} else if err := os.Rename(output, destination); err != nil {
		return install.Record{}, fmt.Errorf("atomically promote converted model: %w", err)
	} else {
		created = true
	}
	source := &install.Record{Repository: plan.Repository, ResolvedSHA: plan.ResolvedSHA, License: plan.License, LicenseURL: plan.LicenseURL, BaseModel: plan.BaseModel, SourceURL: plan.SourceURL}
	record, err := install.RegisterGenerated(root, destination, install.GeneratedMetadata{Filename: plan.OutputFilename, SHA256: outputHash, SizeBytes: info.Size(), DerivedFromSHA256: plan.SourceIdentitySHA256, PreparationPreset: strings.ToUpper(plan.Outtype), PreparationToolchain: plan.EnvironmentID, Origin: "converted", StructuralValidation: "gguf-header-passed-after-conversion", Source: source})
	if err != nil {
		if created {
			_ = os.Remove(destination)
		}
		return install.Record{}, err
	}
	if progress != nil {
		progress("complete", "Converted GGUF passed structural validation and entered managed storage", plan.SourceBytes, plan.SourceBytes)
	}
	return record, nil
}

func Run(ctx context.Context, environment conversionenv.Record, source, output, outtype string, maximumOutput, maximumWorking int64, progress ProgressFunc) error {
	converter, err := os.ReadFile(environment.Converter)
	if err != nil {
		return err
	}
	if strings.Contains(string(converter), "trust_remote_code=True") || strings.Count(string(converter), "trust_remote_code=False") < 7 {
		return errors.New("conversion environment lacks the mandatory no-remote-code converter hardening patch; reinstall it")
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	gguf := filepath.Join(filepath.Dir(environment.Converter), "gguf-py")
	command := exec.CommandContext(child, environment.Python, environment.Converter, source, "--outfile", output, "--outtype", outtype)
	command.Env = append(os.Environ(), "PYTHONNOUSERSITE=1", "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "PYTHONPATH="+environment.SitePackages+string(os.PathListSeparator)+gguf)
	writer := &progressWriter{callback: progress}
	command.Stdout, command.Stderr = writer, writer
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return fmt.Errorf("converter exited with %v%s", err, writer.summary())
			}
			return nil
		case <-ctx.Done():
			cancel()
			<-done
			return ctx.Err()
		case <-ticker.C:
			if info, err := os.Stat(output); err == nil && info.Size() > maximumOutput {
				cancel()
				<-done
				return errors.New("converted output exceeded consented size limit")
			}
			size, err := directorySize(filepath.Dir(source))
			if err != nil {
				cancel()
				<-done
				return err
			}
			if size > maximumWorking {
				cancel()
				<-done
				return errors.New("conversion workspace exceeded consented size limit")
			}
		}
	}
}

func sourceFiles(model hub.Model) ([]SourceFile, int64, error) {
	files := []SourceFile{}
	var weights int64
	for _, file := range model.Siblings {
		name := strings.ToLower(file.Name)
		role := ""
		switch {
		case name == "config.json":
			role = "configuration"
		case isTokenizer(name):
			role = "tokenizer"
		case strings.HasSuffix(name, ".safetensors.index.json"):
			role = "weight index"
		case strings.HasSuffix(name, ".safetensors"):
			role = "weights"
		default:
			continue
		}
		size := file.Size
		if size == 0 && file.LFS != nil {
			size = file.LFS.Size
		}
		if _, err := safeSourcePath("source", file.Name); err != nil || strings.Contains(file.Name, "/") {
			return nil, 0, fmt.Errorf("unsupported source filename %q: only root-level data files are allowed", file.Name)
		}
		if size <= 0 {
			return nil, 0, fmt.Errorf("source file %s has no reported size", file.Name)
		}
		algorithm, digest := "git-sha1", strings.ToLower(file.BlobID)
		if hash := file.ContentSHA256(); len(hash) == 64 {
			algorithm, digest = "sha256", hash
		}
		if (algorithm == "sha256" && len(digest) != 64) || (algorithm == "git-sha1" && len(digest) != 40) {
			return nil, 0, fmt.Errorf("source file %s has no supported immutable content identity", file.Name)
		}
		files = append(files, SourceFile{Name: file.Name, Role: role, SizeBytes: size, VerificationAlgorithm: algorithm, VerificationDigest: digest})
		if role == "weights" {
			weights += size
		}
	}
	if weights == 0 {
		return nil, 0, errors.New("no Safetensors weights selected")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, weights, nil
}
func isTokenizer(name string) bool {
	switch name {
	case "tokenizer.json", "tokenizer.model", "spiece.model", "vocab.txt", "vocab.json", "merges.txt", "tokenizer_config.json", "special_tokens_map.json", "added_tokens.json":
		return true
	}
	return false
}
func downloadAndVerify(ctx context.Context, download DownloadFunc, repository, revision string, file SourceFile, destination string, maximum int64, progress hub.ProgressFunc) error {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	var hasher hash.Hash
	if file.VerificationAlgorithm == "sha256" {
		hasher = sha256.New()
	} else {
		hasher = sha1.New()
		_, _ = io.WriteString(hasher, fmt.Sprintf("blob %d\x00", file.SizeBytes))
	}
	err = download(ctx, repository, revision, file.Name, file.SizeBytes, maximum, io.MultiWriter(output, hasher), progress)
	if err == nil {
		err = output.Sync()
	}
	closeErr := output.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, file.VerificationDigest) {
		_ = os.Remove(destination)
		return fmt.Errorf("source file %s %s mismatch: expected %s, got %s", file.Name, file.VerificationAlgorithm, file.VerificationDigest, actual)
	}
	return nil
}
func verifyFile(filename string, file SourceFile) error {
	info, err := os.Stat(filename)
	if err != nil || info.Size() != file.SizeBytes {
		return errors.New("size changed")
	}
	var hasher hash.Hash
	if file.VerificationAlgorithm == "sha256" {
		hasher = sha256.New()
	} else {
		hasher = sha1.New()
		_, _ = io.WriteString(hasher, fmt.Sprintf("blob %d\x00", file.SizeBytes))
	}
	input, err := os.Open(filename)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(hasher, input)
	closeErr := input.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), file.VerificationDigest) {
		return errors.New("hash changed")
	}
	return nil
}
func sourceIdentity(plan Plan) string {
	value := struct {
		Repository, ResolvedSHA string
		Files                   []SourceFile
	}{plan.Repository, plan.ResolvedSHA, plan.SourceFiles}
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
func sumSizes(files []SourceFile) int64 {
	var total int64
	for _, file := range files {
		total += file.SizeBytes
	}
	return total
}
func safeSourcePath(root, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\\:") {
		return "", errors.New("unsafe source filename")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("source filename escapes workspace")
	}
	return filepath.Join(root, clean), nil
}
func safeRepositoryName(repository string) string {
	name := strings.ReplaceAll(repository, "/", "-")
	var result strings.Builder
	for _, character := range name {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._-", character) {
			result.WriteRune(character)
		} else {
			result.WriteByte('_')
		}
	}
	return result.String()
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
func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

type progressWriter struct {
	mutex    sync.Mutex
	callback ProgressFunc
	tail     []string
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
		if len(writer.tail) > 10 {
			writer.tail = writer.tail[len(writer.tail)-10:]
		}
		if writer.callback != nil && (strings.Contains(line, "INFO") || strings.Contains(line, "Writing") || strings.Contains(line, "error") || strings.Contains(line, "Error")) {
			writer.callback("convert", line, 0, 0)
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
