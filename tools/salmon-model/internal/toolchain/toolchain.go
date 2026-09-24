package toolchain

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/install"
)

const (
	ToolchainID              = "llama-quantize"
	Version                  = "b6002"
	DefaultMaximumArchive    = int64(64 << 20)
	maximumExtractedBytes    = int64(512 << 20)
	maximumArchiveEntryCount = 2048
)

type Artifact struct {
	OS             string
	Arch           string
	Filename       string
	SizeBytes      int64
	SHA256         string
	URL            string
	BinaryRelative string
}

var artifacts = []Artifact{
	{OS: "windows", Arch: "amd64", Filename: "llama-b6002-bin-win-cpu-x64.zip", SizeBytes: 14374016, SHA256: "80b9777a00dc1d88d2fd1c7b4222d337d1c91075f80c6053e53514ccfe66fb06", URL: "https://github.com/ggml-org/llama.cpp/releases/download/b6002/llama-b6002-bin-win-cpu-x64.zip", BinaryRelative: "llama-quantize.exe"},
	{OS: "linux", Arch: "amd64", Filename: "llama-b6002-bin-ubuntu-x64.zip", SizeBytes: 13113096, SHA256: "89a27fc6b537d30d14038b9e4a85423f5fc755b2384c7ce88a9acf61b6dfa2ab", URL: "https://github.com/ggml-org/llama.cpp/releases/download/b6002/llama-b6002-bin-ubuntu-x64.zip", BinaryRelative: "build/bin/llama-quantize"},
	{OS: "darwin", Arch: "amd64", Filename: "llama-b6002-bin-macos-x64.zip", SizeBytes: 28556705, SHA256: "8b181249139d14fc6e77560a401eade0483c031cf7c275e4af9edf06242b6005", URL: "https://github.com/ggml-org/llama.cpp/releases/download/b6002/llama-b6002-bin-macos-x64.zip", BinaryRelative: "build/bin/llama-quantize"},
	{OS: "darwin", Arch: "arm64", Filename: "llama-b6002-bin-macos-arm64.zip", SizeBytes: 11166011, SHA256: "ba38275bba1ceacb161177530ff5e150190e7365bd22920f3577f7cbb6712bb0", URL: "https://github.com/ggml-org/llama.cpp/releases/download/b6002/llama-b6002-bin-macos-arm64.zip", BinaryRelative: "build/bin/llama-quantize"},
}

type Plan struct {
	SchemaVersion int    `json:"schema_version"`
	ToolchainID   string `json:"toolchain_id"`
	Version       string `json:"version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Archive       string `json:"archive"`
	SizeBytes     int64  `json:"size_bytes"`
	SHA256        string `json:"sha256"`
	SourceURL     string `json:"source_url"`
	Destination   string `json:"destination"`
	Binary        string `json:"binary"`
	ConsentDigest string `json:"consent_digest,omitempty"`
	Warning       string `json:"warning"`
}

type Record struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	ToolchainID   string `json:"toolchain_id"`
	Version       string `json:"version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Archive       string `json:"archive"`
	SizeBytes     int64  `json:"size_bytes"`
	SHA256        string `json:"sha256"`
	SourceURL     string `json:"source_url"`
	Path          string `json:"path"`
	Binary        string `json:"binary"`
	BinarySHA256  string `json:"binary_sha256"`
	InstalledAt   string `json:"installed_at"`
	Validation    string `json:"validation"`
}

type ProgressFunc func(completed, total int64)
type DownloadFunc func(context.Context, string, int64, int64, io.Writer, ProgressFunc) error
type ProbeFunc func(context.Context, string) error

type Dependencies struct {
	Download DownloadFunc
	Probe    ProbeFunc
}

func BuildPlan(root string) (Plan, error) {
	return buildPlanFor(root, runtime.GOOS, runtime.GOARCH)
}

func buildPlanFor(root, goos, goarch string) (Plan, error) {
	artifact, err := artifactFor(goos, goarch)
	if err != nil {
		return Plan{}, err
	}
	root, err = resolvedRoot(root)
	if err != nil {
		return Plan{}, err
	}
	destination := filepath.Join(root, "toolchains", ToolchainID, Version, goos+"-"+goarch)
	plan := Plan{
		SchemaVersion: 1, ToolchainID: ToolchainID, Version: Version, OS: goos, Arch: goarch,
		Archive: artifact.Filename, SizeBytes: artifact.SizeBytes, SHA256: artifact.SHA256,
		SourceURL: artifact.URL, Destination: destination, Binary: filepath.Join(destination, filepath.FromSlash(artifact.BinaryRelative)),
		Warning: "This optional executable runs outside the Godot extension. Installation does not authorize quantization or conversion execution.",
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

func Execute(ctx context.Context, plan Plan, consent, root string, maximumBytes int64, progress ProgressFunc, dependencies Dependencies) (Record, error) {
	artifact, err := validatePlan(plan, root)
	if err != nil {
		return Record{}, err
	}
	digest, err := Digest(plan)
	if err != nil {
		return Record{}, err
	}
	if consent == "" || !strings.EqualFold(consent, digest) || !strings.EqualFold(plan.ConsentDigest, digest) {
		return Record{}, errors.New("consent digest does not match the exact toolchain installation plan")
	}
	if maximumBytes <= 0 {
		maximumBytes = DefaultMaximumArchive
	}
	if plan.SizeBytes > maximumBytes {
		return Record{}, errors.New("planned toolchain archive exceeds the configured maximum size")
	}
	if dependencies.Download == nil {
		dependencies.Download = Download
	}
	if dependencies.Probe == nil {
		dependencies.Probe = Probe
	}
	root, _ = resolvedRoot(root)
	if existing, found, err := readRecord(root, recordID(plan)); err != nil {
		return Record{}, err
	} else if found {
		if err := validateExisting(existing, plan, dependencies.Probe, ctx); err != nil {
			return Record{}, err
		}
		return existing, nil
	}
	if _, err := os.Stat(plan.Destination); err == nil {
		return Record{}, errors.New("toolchain destination exists without a valid registry record")
	} else if !os.IsNotExist(err) {
		return Record{}, err
	}
	downloadDirectory := filepath.Join(root, "downloads")
	if err := os.MkdirAll(downloadDirectory, 0o755); err != nil {
		return Record{}, err
	}
	temporaryArchive := filepath.Join(downloadDirectory, "toolchain-"+digest+".part")
	staging := plan.Destination + ".staging-" + digest[:12]
	_ = os.Remove(temporaryArchive)
	_ = os.RemoveAll(staging)
	defer os.Remove(temporaryArchive)
	defer os.RemoveAll(staging)
	archive, err := os.OpenFile(temporaryArchive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Record{}, err
	}
	hasher := sha256.New()
	err = dependencies.Download(ctx, artifact.URL, artifact.SizeBytes, maximumBytes, io.MultiWriter(archive, hasher), progress)
	if err != nil {
		archive.Close()
		return Record{}, err
	}
	if err := archive.Sync(); err != nil {
		archive.Close()
		return Record{}, err
	}
	if err := archive.Close(); err != nil {
		return Record{}, err
	}
	if actual := hex.EncodeToString(hasher.Sum(nil)); !strings.EqualFold(actual, artifact.SHA256) {
		return Record{}, fmt.Errorf("toolchain archive SHA-256 mismatch: expected %s, got %s", artifact.SHA256, actual)
	}
	if err := extractZIP(temporaryArchive, staging); err != nil {
		return Record{}, err
	}
	stagedBinary := filepath.Join(staging, filepath.FromSlash(artifact.BinaryRelative))
	if err := dependencies.Probe(ctx, stagedBinary); err != nil {
		return Record{}, fmt.Errorf("toolchain executable probe failed: %w", err)
	}
	binaryHash, err := hashFile(stagedBinary)
	if err != nil {
		return Record{}, fmt.Errorf("hash toolchain executable: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(plan.Destination), 0o755); err != nil {
		return Record{}, err
	}
	if err := os.Rename(staging, plan.Destination); err != nil {
		return Record{}, fmt.Errorf("atomically install toolchain: %w", err)
	}
	record := Record{
		SchemaVersion: 1, ID: recordID(plan), ToolchainID: ToolchainID, Version: Version,
		OS: plan.OS, Arch: plan.Arch, Archive: plan.Archive, SizeBytes: plan.SizeBytes,
		SHA256: strings.ToLower(plan.SHA256), SourceURL: plan.SourceURL, Path: plan.Destination,
		Binary: plan.Binary, BinarySHA256: binaryHash, InstalledAt: time.Now().UTC().Format(time.RFC3339), Validation: "quantize-help-probe-passed",
	}
	if err := writeRecord(root, record); err != nil {
		_ = os.RemoveAll(plan.Destination)
		return Record{}, err
	}
	return record, nil
}

func Download(ctx context.Context, sourceURL string, expectedSize, maximumBytes int64, destination io.Writer, progress ProgressFunc) error {
	client := &http.Client{Timeout: 0, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return errors.New("toolchain download redirect must use HTTPS")
		}
		if len(via) >= 10 {
			return errors.New("too many toolchain download redirects")
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	if request.URL.Scheme != "https" {
		return errors.New("toolchain download must use HTTPS")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("toolchain download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maximumBytes || response.ContentLength > expectedSize {
		return errors.New("toolchain response exceeds the consented size")
	}
	limited := &countingWriter{writer: destination, total: expectedSize, progress: progress}
	written, err := io.Copy(limited, io.LimitReader(response.Body, maximumBytes+1))
	if err != nil {
		return err
	}
	if written > maximumBytes {
		return errors.New("toolchain download exceeded the configured maximum size")
	}
	if written != expectedSize {
		return fmt.Errorf("toolchain download size mismatch: expected %d, got %d", expectedSize, written)
	}
	return nil
}

func Probe(ctx context.Context, binary string) error {
	command := exec.CommandContext(ctx, binary, "--help")
	directory := filepath.Dir(binary)
	command.Env = append(os.Environ(), "LD_LIBRARY_PATH="+directory, "DYLD_LIBRARY_PATH="+directory)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	text := string(output)
	if !strings.Contains(text, "Allowed quantization types") || !strings.Contains(text, "Q4_K_M") {
		return fmt.Errorf("unexpected llama-quantize help output: %s", truncate(text, 300))
	}
	// b6002 exits non-zero after printing usage. Content validation is authoritative here.
	_ = err
	return nil
}

func Resolve(ctx context.Context, root string) (Record, error) {
	plan, err := BuildPlan(root)
	if err != nil {
		return Record{}, err
	}
	root, err = resolvedRoot(root)
	if err != nil {
		return Record{}, err
	}
	record, found, err := readRecord(root, recordID(plan))
	if err != nil {
		return Record{}, err
	}
	if !found {
		return Record{}, errors.New("llama-quantize toolchain is not installed; run toolchain-plan and toolchain-install first")
	}
	if err := validateExisting(record, plan, Probe, ctx); err != nil {
		return Record{}, err
	}
	return record, nil
}

func List(root string) ([]Record, error) {
	root, err := resolvedRoot(root)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(root, "toolchains", "registry")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		record, found, err := readRecord(root, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		if found {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records, nil
}

func Remove(root, id string) (Record, error) {
	root, err := resolvedRoot(root)
	if err != nil {
		return Record{}, err
	}
	record, found, err := readRecord(root, id)
	if err != nil {
		return Record{}, err
	}
	if !found {
		return Record{}, os.ErrNotExist
	}
	artifact, err := artifactFor(record.OS, record.Arch)
	if err != nil || record.ID != recordIDFrom(record.ToolchainID, record.Version, record.OS, record.Arch) || record.Archive != artifact.Filename {
		return Record{}, errors.New("toolchain registry identity is invalid")
	}
	expectedRoot := filepath.Join(root, "toolchains", ToolchainID, Version, record.OS+"-"+record.Arch)
	if filepath.Clean(record.Path) != filepath.Clean(expectedRoot) || filepath.Clean(record.Binary) != filepath.Join(expectedRoot, filepath.FromSlash(artifact.BinaryRelative)) {
		return Record{}, errors.New("toolchain registry path escapes managed storage")
	}
	recordPath := filepath.Join(root, "toolchains", "registry", id+".json")
	deleting := recordPath + ".deleting"
	if err := os.Rename(recordPath, deleting); err != nil {
		return Record{}, err
	}
	if err := os.RemoveAll(expectedRoot); err != nil {
		_ = os.Rename(deleting, recordPath)
		return Record{}, err
	}
	if err := os.Remove(deleting); err != nil {
		return Record{}, err
	}
	return record, nil
}

func artifactFor(goos, goarch string) (Artifact, error) {
	for _, artifact := range artifacts {
		if artifact.OS == goos && artifact.Arch == goarch {
			return artifact, nil
		}
	}
	return Artifact{}, fmt.Errorf("llama-quantize %s has no pinned artifact for %s/%s", Version, goos, goarch)
}

func validatePlan(plan Plan, root string) (Artifact, error) {
	artifact, err := artifactFor(plan.OS, plan.Arch)
	if err != nil {
		return Artifact{}, err
	}
	expected, err := buildPlanFor(root, plan.OS, plan.Arch)
	if err != nil {
		return Artifact{}, err
	}
	providedConsent := plan.ConsentDigest
	plan.ConsentDigest = ""
	expected.ConsentDigest = ""
	if plan != expected {
		return Artifact{}, errors.New("toolchain plan does not match the pinned artifact catalog and destination")
	}
	if providedConsent == "" {
		return Artifact{}, errors.New("toolchain plan is missing its consent digest")
	}
	return artifact, nil
}

func validateExisting(record Record, plan Plan, probe ProbeFunc, ctx context.Context) error {
	if record.ID != recordID(plan) || record.SHA256 != strings.ToLower(plan.SHA256) || filepath.Clean(record.Path) != filepath.Clean(plan.Destination) || filepath.Clean(record.Binary) != filepath.Clean(plan.Binary) || len(record.BinarySHA256) != 64 {
		return errors.New("installed toolchain registry record conflicts with the requested plan")
	}
	binaryHash, err := hashFile(record.Binary)
	if err != nil {
		return fmt.Errorf("hash installed toolchain executable: %w", err)
	}
	if !strings.EqualFold(binaryHash, record.BinarySHA256) {
		return errors.New("installed toolchain executable hash does not match its registry record")
	}
	if err := probe(ctx, record.Binary); err != nil {
		return fmt.Errorf("installed toolchain is corrupt or unusable: %w", err)
	}
	return nil
}

func extractZIP(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open toolchain ZIP: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > maximumArchiveEntryCount {
		return errors.New("toolchain ZIP contains too many entries")
	}
	var declaredTotal, extractedTotal int64
	for _, entry := range reader.File {
		if entry.UncompressedSize64 > uint64(maximumExtractedBytes) || declaredTotal > maximumExtractedBytes-int64(entry.UncompressedSize64) {
			return errors.New("toolchain ZIP exceeds the maximum extracted size")
		}
		declaredTotal += int64(entry.UncompressedSize64)
		if entry.Mode()&os.ModeSymlink != 0 || entry.Mode()&os.ModeType != 0 && !entry.FileInfo().IsDir() {
			return errors.New("toolchain ZIP contains an unsupported special entry")
		}
		clean, err := safeArchivePath(entry.Name)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(clean))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		mode := entry.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			source.Close()
			return err
		}
		remaining := maximumExtractedBytes - extractedTotal
		written, copyErr := io.Copy(targetFile, io.LimitReader(source, remaining+1))
		closeTargetErr := targetFile.Close()
		closeSourceErr := source.Close()
		if copyErr != nil || closeTargetErr != nil || closeSourceErr != nil {
			return errors.Join(copyErr, closeTargetErr, closeSourceErr)
		}
		if written > remaining {
			return errors.New("toolchain ZIP exceeded the maximum extracted size")
		}
		if written != int64(entry.UncompressedSize64) {
			return errors.New("toolchain ZIP entry size mismatch")
		}
		extractedTotal += written
	}
	return nil
}

func safeArchivePath(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\\:") || strings.HasPrefix(name, "/") {
		return "", errors.New("toolchain ZIP contains an unsafe path")
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("toolchain ZIP contains a path traversal")
	}
	return clean, nil
}

func writeRecord(root string, record Record) error {
	directory := filepath.Join(root, "toolchains", "registry")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary := filepath.Join(directory, record.ID+".json.tmp")
	final := filepath.Join(directory, record.ID+".json")
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, final); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func readRecord(root, id string) (Record, bool, error) {
	if id == "" || strings.ContainsAny(id, `/\\`) {
		return Record{}, false, errors.New("invalid toolchain record ID")
	}
	data, err := os.ReadFile(filepath.Join(root, "toolchains", "registry", id+".json"))
	if os.IsNotExist(err) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, false, fmt.Errorf("invalid toolchain record %s: %w", id, err)
	}
	return record, true, nil
}

func recordID(plan Plan) string {
	return recordIDFrom(plan.ToolchainID, plan.Version, plan.OS, plan.Arch)
}

func recordIDFrom(toolchainID, version, goos, goarch string) string {
	return toolchainID + "-" + version + "-" + goos + "-" + goarch
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

type countingWriter struct {
	writer   io.Writer
	written  int64
	total    int64
	progress ProgressFunc
}

func (writer *countingWriter) Write(data []byte) (int, error) {
	count, err := writer.writer.Write(data)
	writer.written += int64(count)
	if writer.progress != nil {
		writer.progress(writer.written, writer.total)
	}
	return count, err
}

func hashFile(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func truncate(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maximum {
		return value
	}
	return value[:maximum] + "…"
}
