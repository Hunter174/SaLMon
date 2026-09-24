package conversionenv

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/install"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/toolchain"
)

const DefaultMaximumComponentBytes int64 = 128 << 20

type Plan struct {
	SchemaVersion                  int        `json:"schema_version"`
	ToolchainID                    string     `json:"toolchain_id"`
	Version                        string     `json:"version"`
	OS                             string     `json:"os"`
	Arch                           string     `json:"arch"`
	UVVersion                      string     `json:"uv_version"`
	PythonVersion                  string     `json:"python_version"`
	ConverterCommit                string     `json:"converter_commit"`
	Artifacts                      []Artifact `json:"artifacts"`
	DependencyLockSHA256           string     `json:"dependency_lock_sha256"`
	Dependencies                   []string   `json:"dependencies"`
	MaximumDependencyDownloadBytes int64      `json:"maximum_dependency_download_bytes"`
	EstimatedInstalledBytes        int64      `json:"estimated_installed_bytes"`
	Destination                    string     `json:"destination"`
	ConsentDigest                  string     `json:"consent_digest,omitempty"`
	Warning                        string     `json:"warning"`
}

type Record struct {
	SchemaVersion        int    `json:"schema_version"`
	ID                   string `json:"id"`
	ToolchainID          string `json:"toolchain_id"`
	Version              string `json:"version"`
	OS                   string `json:"os"`
	Arch                 string `json:"arch"`
	UVVersion            string `json:"uv_version"`
	PythonVersion        string `json:"python_version"`
	ConverterCommit      string `json:"converter_commit"`
	DependencyLockSHA256 string `json:"dependency_lock_sha256"`
	Path                 string `json:"path"`
	Python               string `json:"python"`
	PythonSHA256         string `json:"python_sha256"`
	Converter            string `json:"converter"`
	ConverterSHA256      string `json:"converter_sha256"`
	SitePackages         string `json:"site_packages"`
	SitePackagesSHA256   string `json:"site_packages_sha256"`
	InstalledAt          string `json:"installed_at"`
	Validation           string `json:"validation"`
}

type ProgressFunc func(stage, message string, completed, total int64)
type DownloadFunc func(context.Context, string, int64, int64, io.Writer, toolchain.ProgressFunc) error
type InstallFunc func(context.Context, string, string, string, string, int64, int64, ProgressFunc) error
type ProbeFunc func(context.Context, string, string, string, string) error

type Dependencies struct {
	Download DownloadFunc
	Install  InstallFunc
	Probe    ProbeFunc
}

func BuildPlan(root string) (Plan, error) { return buildPlanFor(root, runtime.GOOS, runtime.GOARCH) }

func buildPlanFor(root, goos, goarch string) (Plan, error) {
	platform, err := platformFor(goos, goarch)
	if err != nil {
		return Plan{}, err
	}
	_, lockHash, packages, err := lockFor(platform)
	if err != nil {
		return Plan{}, err
	}
	root, err = resolvedRoot(root)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{SchemaVersion: 1, ToolchainID: ToolchainID, Version: Version, OS: goos, Arch: goarch,
		UVVersion: UVVersion, PythonVersion: PythonVersion, ConverterCommit: ConverterCommit,
		Artifacts: []Artifact{platform.UV, platform.Python, sourceArtifact}, DependencyLockSHA256: lockHash,
		Dependencies: packages, MaximumDependencyDownloadBytes: platform.MaximumDependencyBytes,
		EstimatedInstalledBytes: platform.EstimatedInstalledBytes,
		Destination:             filepath.Join(root, "toolchains", ToolchainID, Version, goos+"-"+goarch),
		Warning:                 "This optional isolated environment is large. It installs only hash-locked binary wheels and pinned converter source; it does not authorize model downloads or conversion execution."}
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

func Execute(ctx context.Context, plan Plan, consent, root string, maximumComponentBytes int64, progress ProgressFunc, dependencies Dependencies) (Record, error) {
	expected, platform, lock, err := validatePlan(plan, root)
	if err != nil {
		return Record{}, err
	}
	digest, err := Digest(plan)
	if err != nil {
		return Record{}, err
	}
	if consent == "" || !strings.EqualFold(consent, digest) || !strings.EqualFold(plan.ConsentDigest, digest) {
		return Record{}, errors.New("consent digest does not match the exact conversion-environment plan")
	}
	if maximumComponentBytes <= 0 {
		maximumComponentBytes = DefaultMaximumComponentBytes
	}
	for _, artifact := range plan.Artifacts {
		if artifact.SizeBytes > maximumComponentBytes {
			return Record{}, fmt.Errorf("artifact %s exceeds the configured component limit", artifact.Name)
		}
	}
	if dependencies.Download == nil {
		dependencies.Download = toolchain.Download
	}
	if dependencies.Install == nil {
		dependencies.Install = InstallDependencies
	}
	if dependencies.Probe == nil {
		dependencies.Probe = probe
	}
	root, _ = resolvedRoot(root)
	if record, found, err := readRecord(root, recordID(plan)); err != nil {
		return Record{}, err
	} else if found {
		if err := validateExisting(ctx, record, expected, dependencies.Probe); err != nil {
			return Record{}, err
		}
		return record, nil
	}
	if _, err := os.Stat(plan.Destination); err == nil {
		return Record{}, errors.New("conversion environment destination exists without a valid registry record")
	} else if !os.IsNotExist(err) {
		return Record{}, err
	}
	downloadDir := filepath.Join(root, "downloads")
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		return Record{}, err
	}
	staging := plan.Destination + ".staging-" + digest[:12]
	_ = os.RemoveAll(staging)
	defer os.RemoveAll(staging)
	for index, artifact := range plan.Artifacts {
		if progress != nil {
			progress("download", artifact.Name, 0, artifact.SizeBytes)
		}
		temporary := filepath.Join(downloadDir, fmt.Sprintf("conversion-env-%s-%d.part", digest, index))
		_ = os.Remove(temporary)
		if err := downloadArtifact(ctx, artifact, temporary, maximumComponentBytes, progress, dependencies.Download); err != nil {
			_ = os.Remove(temporary)
			return Record{}, err
		}
		component := filepath.Join(staging, artifact.Kind)
		if err := extractArchive(temporary, component, artifact.Format); err != nil {
			_ = os.Remove(temporary)
			return Record{}, fmt.Errorf("extract %s: %w", artifact.Name, err)
		}
		if artifact.Kind == "converter-source" {
			if err := pruneConverterSource(component); err != nil {
				_ = os.Remove(temporary)
				return Record{}, err
			}
		}
		_ = os.Remove(temporary)
	}
	lockPath := filepath.Join(staging, "requirements.lock")
	if err := os.WriteFile(lockPath, lock, 0o600); err != nil {
		return Record{}, err
	}
	uv, err := findExecutable(filepath.Join(staging, "uv"), executableName("uv"))
	if err != nil {
		return Record{}, err
	}
	basePython := filepath.Join(staging, "python", "python", basePythonRelative())
	if info, err := os.Stat(basePython); err != nil || !info.Mode().IsRegular() {
		return Record{}, errors.New("pinned Python archive is missing its expected executable")
	}
	converter, err := findFile(filepath.Join(staging, "converter-source"), "convert_hf_to_gguf.py")
	if err != nil {
		return Record{}, err
	}
	ggufPath := filepath.Join(filepath.Dir(converter), "gguf-py")
	sitePackages := filepath.Join(staging, "site-packages")
	if err := dependencies.Install(ctx, uv, basePython, sitePackages, lockPath, plan.MaximumDependencyDownloadBytes, plan.EstimatedInstalledBytes, progress); err != nil {
		return Record{}, err
	}
	if err := dependencies.Probe(ctx, basePython, converter, ggufPath, sitePackages); err != nil {
		return Record{}, fmt.Errorf("conversion environment probe failed: %w", err)
	}
	pythonHash, err := hashFile(basePython)
	if err != nil {
		return Record{}, err
	}
	converterHash, err := hashFile(converter)
	if err != nil {
		return Record{}, err
	}
	sitePackagesHash, err := hashTree(sitePackages)
	if err != nil {
		return Record{}, fmt.Errorf("hash installed conversion dependencies: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(plan.Destination), 0o755); err != nil {
		return Record{}, err
	}
	if err := os.Rename(staging, plan.Destination); err != nil {
		return Record{}, fmt.Errorf("atomically install conversion environment: %w", err)
	}
	pythonRelative, _ := filepath.Rel(staging, basePython)
	finalPython := filepath.Join(plan.Destination, pythonRelative)
	finalConverterRelative, _ := filepath.Rel(staging, converter)
	finalConverter := filepath.Join(plan.Destination, finalConverterRelative)
	finalSitePackages := filepath.Join(plan.Destination, "site-packages")
	record := Record{SchemaVersion: 1, ID: recordID(plan), ToolchainID: ToolchainID, Version: Version, OS: plan.OS, Arch: plan.Arch,
		UVVersion: UVVersion, PythonVersion: PythonVersion, ConverterCommit: ConverterCommit, DependencyLockSHA256: plan.DependencyLockSHA256,
		Path: plan.Destination, Python: finalPython, PythonSHA256: pythonHash, Converter: finalConverter, ConverterSHA256: converterHash, SitePackages: finalSitePackages, SitePackagesSHA256: sitePackagesHash,
		InstalledAt: time.Now().UTC().Format(time.RFC3339), Validation: "isolated-import-and-converter-help-probe-passed"}
	if err := writeRecord(root, record); err != nil {
		_ = os.RemoveAll(plan.Destination)
		return Record{}, err
	}
	_ = platform
	return record, nil
}

func InstallDependencies(ctx context.Context, uv, python, sitePackages, lock string, maximumDependencyBytes, maximumInstalledBytes int64, progress ProgressFunc) error {
	if progress != nil {
		progress("environment", "Preparing isolated Python package directory", 0, 0)
	}
	working := filepath.Dir(sitePackages)
	cache := filepath.Join(working, "dependency-cache")
	temporary := filepath.Join(working, "dependency-temp")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(temporary, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(cache)
	defer os.RemoveAll(temporary)
	environment := append(os.Environ(), "PYTHONNOUSERSITE=1", "UV_NO_PROGRESS=1", "UV_LINK_MODE=copy", "UV_PYTHON_DOWNLOADS=never", "UV_CACHE_DIR="+cache, "TMP="+temporary, "TEMP="+temporary, "TMPDIR="+temporary)
	if err := os.MkdirAll(sitePackages, 0o755); err != nil {
		return err
	}
	if progress != nil {
		progress("dependencies", "Installing hash-locked CPU conversion dependencies", 0, maximumDependencyBytes)
	}
	if err := runBounded(ctx, environment, working, []string{cache, temporary}, maximumInstalledBytes+maximumDependencyBytes, maximumDependencyBytes, uv, "pip", "install", "--python", python, "--target", sitePackages, "--require-hashes", "--only-binary", ":all:", "--torch-backend", "cpu", "-r", lock); err != nil {
		return fmt.Errorf("install locked dependencies: %w", err)
	}
	return nil
}

func Resolve(ctx context.Context, root string) (Record, error) {
	plan, err := BuildPlan(root)
	if err != nil {
		return Record{}, err
	}
	root, _ = resolvedRoot(root)
	record, found, err := readRecord(root, recordID(plan))
	if err != nil {
		return Record{}, err
	}
	if !found {
		return Record{}, errors.New("conversion environment is not installed; run conversion-toolchain-plan and conversion-toolchain-install first")
	}
	if err := validateExisting(ctx, record, plan, probe); err != nil {
		return Record{}, err
	}
	return record, nil
}

func List(root string) ([]Record, error) {
	root, err := resolvedRoot(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "toolchains", "conversion-registry"))
	if os.IsNotExist(err) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := []Record{}
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
	if id != recordIDFrom(record.OS, record.Arch) {
		return Record{}, errors.New("conversion environment registry identity is invalid")
	}
	expected := filepath.Join(root, "toolchains", ToolchainID, Version, record.OS+"-"+record.Arch)
	if filepath.Clean(record.Path) != filepath.Clean(expected) {
		return Record{}, errors.New("conversion environment path escapes managed storage")
	}
	recordPath := filepath.Join(root, "toolchains", "conversion-registry", id+".json")
	deleting := recordPath + ".deleting"
	if err := os.Rename(recordPath, deleting); err != nil {
		return Record{}, err
	}
	if err := os.RemoveAll(expected); err != nil {
		_ = os.Rename(deleting, recordPath)
		return Record{}, err
	}
	if err := os.Remove(deleting); err != nil {
		return Record{}, err
	}
	return record, nil
}

func validatePlan(plan Plan, root string) (Plan, Platform, []byte, error) {
	expected, err := buildPlanFor(root, plan.OS, plan.Arch)
	if err != nil {
		return Plan{}, Platform{}, nil, err
	}
	supplied := plan.ConsentDigest
	plan.ConsentDigest = ""
	expected.ConsentDigest = ""
	if !plansEqual(plan, expected) || supplied == "" {
		return Plan{}, Platform{}, nil, errors.New("conversion environment plan does not match the pinned catalog, lock, and destination")
	}
	platform, _ := platformFor(plan.OS, plan.Arch)
	lock, _, _, err := lockFor(platform)
	return expected, platform, lock, err
}

func plansEqual(a, b Plan) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func validateExisting(ctx context.Context, record Record, plan Plan, probeFunction ProbeFunc) error {
	if record.ID != recordID(plan) || record.DependencyLockSHA256 != plan.DependencyLockSHA256 || filepath.Clean(record.Path) != filepath.Clean(plan.Destination) || len(record.PythonSHA256) != 64 || len(record.ConverterSHA256) != 64 {
		return errors.New("installed conversion environment conflicts with the pinned plan")
	}
	pythonHash, err := hashFile(record.Python)
	if err != nil {
		return err
	}
	converterHash, err := hashFile(record.Converter)
	if err != nil {
		return err
	}
	if !strings.EqualFold(pythonHash, record.PythonSHA256) || !strings.EqualFold(converterHash, record.ConverterSHA256) {
		return errors.New("installed conversion environment file hash does not match its registry record")
	}
	ggufPath := filepath.Join(filepath.Dir(record.Converter), "gguf-py")
	if filepath.Clean(record.SitePackages) != filepath.Join(record.Path, "site-packages") || len(record.SitePackagesSHA256) != 64 {
		return errors.New("conversion environment package path or identity is invalid")
	}
	siteHash, err := hashTree(record.SitePackages)
	if err != nil {
		return err
	}
	if !strings.EqualFold(siteHash, record.SitePackagesSHA256) {
		return errors.New("installed conversion dependency hash does not match its registry record")
	}
	return probeFunction(ctx, record.Python, record.Converter, ggufPath, record.SitePackages)
}

func downloadArtifact(ctx context.Context, artifact Artifact, destination string, maximum int64, progress ProgressFunc, download DownloadFunc) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	err = download(ctx, artifact.URL, artifact.SizeBytes, maximum, io.MultiWriter(file, hasher), func(done, total int64) {
		if progress != nil {
			progress("download", artifact.Name, done, total)
		}
	})
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, artifact.SHA256) {
		return fmt.Errorf("artifact %s SHA-256 mismatch", artifact.Name)
	}
	return nil
}

func probe(ctx context.Context, python, converter, ggufPath, sitePackages string) error {
	environment := append(os.Environ(), "PYTHONNOUSERSITE=1", "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "PYTHONPATH="+sitePackages+string(os.PathListSeparator)+ggufPath)
	if err := run(ctx, environment, python, "-c", "import numpy, sentencepiece, transformers, torch, google.protobuf, gguf; print(torch.__version__)"); err != nil {
		return err
	}
	return run(ctx, environment, python, converter, "--help")
}

func run(ctx context.Context, environment []string, executable string, arguments ...string) error {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %w: %s", arguments, err, truncate(string(output), 1200))
	}
	return nil
}
func runBounded(ctx context.Context, environment []string, directory string, transientDirectories []string, maximumWorking, maximumTransient int64, executable string, arguments ...string) error {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(child, executable, arguments...)
	command.Env = environment
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return fmt.Errorf("%v: %w: %s", arguments, err, truncate(output.String(), 1200))
			}
			return nil
		case <-ctx.Done():
			cancel()
			<-done
			return ctx.Err()
		case <-ticker.C:
			size, err := directorySize(directory)
			if err != nil {
				cancel()
				<-done
				return err
			}
			if size > maximumWorking {
				cancel()
				<-done
				return fmt.Errorf("conversion environment exceeded consented working-size limit of %d bytes", maximumWorking)
			}
			var transient int64
			for _, candidate := range transientDirectories {
				value, err := directorySize(candidate)
				if err != nil {
					cancel()
					<-done
					return err
				}
				transient += value
			}
			if transient > maximumTransient {
				cancel()
				<-done
				return fmt.Errorf("conversion dependency downloads exceeded consented limit of %d bytes", maximumTransient)
			}
		}
	}
}
func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			// uv creates and removes locked temporary files while this advisory
			// size monitor walks the staging tree, especially on Windows.
			return nil
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func extractArchive(archive, destination, format string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	if format == "zip" {
		return extractZIP(archive, destination)
	}
	if format == "tar.gz" {
		return extractTarGZ(archive, destination)
	}
	return errors.New("unsupported pinned archive format")
}

func extractZIP(filename, destination string) error {
	reader, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer reader.Close()
	var total int64
	for _, entry := range reader.File {
		clean, err := safeArchivePath(entry.Name)
		if err != nil {
			return err
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return errors.New("ZIP symlinks are not allowed")
		}
		total += int64(entry.UncompressedSize64)
		if total > MaximumExtractedBytes {
			return errors.New("archive exceeds expanded-size limit")
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
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			source.Close()
			return err
		}
		written, copyErr := io.Copy(out, source)
		closeErr := out.Close()
		source.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		if written != int64(entry.UncompressedSize64) {
			return errors.New("ZIP entry size mismatch")
		}
	}
	return nil
}

func extractTarGZ(filename, destination string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var total int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		clean, err := safeArchivePath(header.Name)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(clean))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			total += header.Size
			if total > MaximumExtractedBytes {
				return errors.New("archive exceeds expanded-size limit")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			written, copyErr := io.CopyN(out, reader, header.Size)
			closeErr := out.Close()
			if copyErr != nil || closeErr != nil {
				return errors.Join(copyErr, closeErr)
			}
			if written != header.Size {
				return errors.New("tar entry size mismatch")
			}
		case tar.TypeSymlink:
			if err := safeSymlink(destination, clean, header.Linkname, target); err != nil {
				return err
			}
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			// Metadata records are consumed by archive/tar and create no filesystem entry.
			continue
		default:
			return fmt.Errorf("unsupported tar entry type %d", header.Typeflag)
		}
	}
	return nil
}

func safeArchivePath(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\\:") || strings.HasPrefix(name, "/") {
		return "", errors.New("archive contains an unsafe path")
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("archive contains path traversal")
	}
	return clean, nil
}
func safeSymlink(root, name, link, target string) error {
	if link == "" || strings.Contains(link, "\\") || path.IsAbs(link) {
		return errors.New("archive contains unsafe symlink")
	}
	resolved := path.Clean(path.Join(path.Dir(name), link))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return errors.New("archive symlink escapes destination")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Symlink(filepath.FromSlash(link), target)
}

func pruneConverterSource(component string) error {
	converter, err := findFile(component, "convert_hf_to_gguf.py")
	if err != nil {
		return err
	}
	sourceRoot := filepath.Dir(converter)
	gguf := filepath.Join(sourceRoot, "gguf-py")
	if info, err := os.Stat(gguf); err != nil || !info.IsDir() {
		return errors.New("pinned converter source is missing gguf-py")
	}
	minimal := component + ".minimal"
	_ = os.RemoveAll(minimal)
	if err := os.MkdirAll(filepath.Join(minimal, "gguf-py"), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(converter)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(minimal, "convert_hf_to_gguf.py"), data, 0o600); err != nil {
		return err
	}
	if license, err := os.ReadFile(filepath.Join(sourceRoot, "LICENSE")); err == nil {
		if err := os.WriteFile(filepath.Join(minimal, "LICENSE"), license, 0o600); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := copyTree(gguf, filepath.Join(minimal, "gguf-py")); err != nil {
		return err
	}
	if err := os.RemoveAll(component); err != nil {
		return err
	}
	return os.Rename(minimal, component)
}
func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(filename string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, filename)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("converter source contains an unexpected symlink")
		}
		if !entry.Type().IsRegular() {
			return errors.New("converter source contains a special file")
		}
		input, err := os.Open(filename)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		return errors.Join(copyErr, closeOutputErr, closeInputErr)
	})
}

func findExecutable(root, name string) (string, error) { return findFile(root, name) }
func findFile(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), name) {
			if found != "" {
				return fmt.Errorf("multiple %s files in pinned archive", name)
			}
			found = p
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("pinned archive is missing %s", name)
	}
	return found, nil
}
func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}
func basePythonRelative() string {
	if runtime.GOOS == "windows" {
		return "python.exe"
	}
	return filepath.Join("bin", "python3.11")
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
func recordID(plan Plan) string { return recordIDFrom(plan.OS, plan.Arch) }
func recordIDFrom(goos, goarch string) string {
	return ToolchainID + "-" + Version + "-" + goos + "-" + goarch
}
func registryPath(root, id string) string {
	return filepath.Join(root, "toolchains", "conversion-registry", id+".json")
}
func writeRecord(root string, record Record) error {
	directory := filepath.Dir(registryPath(root, record.ID))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary := registryPath(root, record.ID) + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, registryPath(root, record.ID)); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
func readRecord(root, id string) (Record, bool, error) {
	if id == "" || strings.ContainsAny(id, `/\\`) {
		return Record{}, false, errors.New("invalid conversion environment record ID")
	}
	data, err := os.ReadFile(registryPath(root, id))
	if os.IsNotExist(err) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}
func hashTree(root string) (string, error) {
	hasher := sha256.New()
	err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("installed dependency tree contains a symlink or special file")
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(hasher, filepath.ToSlash(relative)+"\x00"); err != nil {
			return err
		}
		file, err := os.Open(filename)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(hasher, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		_, err = hasher.Write([]byte{0})
		return err
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
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
func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
