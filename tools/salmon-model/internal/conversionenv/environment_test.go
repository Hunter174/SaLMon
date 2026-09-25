package conversionenv

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/toolchain"
)

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o755)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func useFakeCatalog(t *testing.T) (map[string][]byte, Platform) {
	t.Helper()
	originalPlatforms := append([]Platform(nil), platforms...)
	originalSource := sourceArtifact
	t.Cleanup(func() { platforms = originalPlatforms; sourceArtifact = originalSource })
	index := -1
	for i := range platforms {
		if platforms[i].OS == runtime.GOOS && platforms[i].Arch == runtime.GOARCH {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatalf("unsupported test platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	uv := zipBytes(t, map[string]string{executableName("uv"): "fake uv"})
	python := zipBytes(t, map[string]string{filepath.ToSlash(filepath.Join("python", basePythonRelative())): "fake base python"})
	source := zipBytes(t, map[string]string{"convert_hf_to_gguf.py": strings.Repeat("trust_remote_code=True\n", 7), "gguf-py/gguf/__init__.py": "fake gguf"})
	contents := map[string][]byte{"https://test/uv": uv, "https://test/python": python, "https://test/source": source}
	makeArtifact := func(kind, url string, data []byte) Artifact {
		hash := sha256.Sum256(data)
		return Artifact{Name: kind + ".zip", Kind: kind, SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(hash[:]), URL: url, Format: "zip"}
	}
	platforms[index].UV = makeArtifact("uv", "https://test/uv", uv)
	platforms[index].Python = makeArtifact("python", "https://test/python", python)
	platforms[index].MaximumDependencyWorkingBytes = 1 << 20
	platforms[index].EstimatedInstalledBytes = 2 << 20
	sourceArtifact = makeArtifact("converter-source", "https://test/source", source)
	return contents, platforms[index]
}

func dependenciesFor(contents map[string][]byte, installError error) Dependencies {
	return Dependencies{
		Download: func(ctx context.Context, url string, expected, maximum int64, destination io.Writer, progress toolchain.ProgressFunc) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			data, ok := contents[url]
			if !ok {
				return errors.New("unexpected URL")
			}
			if int64(len(data)) != expected || expected > maximum {
				return errors.New("unexpected bounds")
			}
			_, err := destination.Write(data)
			if progress != nil {
				progress(expected, expected)
			}
			return err
		},
		Install: func(_ context.Context, _, _, sitePackages, _ string, _, _ int64, progress ProgressFunc) error {
			if installError != nil {
				return installError
			}
			if err := os.MkdirAll(sitePackages, 0o755); err != nil {
				return err
			}
			if progress != nil {
				progress("dependencies", "fake install", 1, 1)
			}
			return os.WriteFile(filepath.Join(sitePackages, "installed"), []byte("fake packages"), 0o600)
		},
		Probe: func(_ context.Context, python, converter, gguf, sitePackages string) error {
			for _, name := range []string{python, converter, filepath.Join(gguf, "gguf", "__init__.py"), filepath.Join(sitePackages, "installed")} {
				if _, err := os.Stat(name); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func TestVerifiedEnvironmentInstallListIdempotenceAndRemove(t *testing.T) {
	contents, _ := useFakeCatalog(t)
	root := t.TempDir()
	plan, err := BuildPlan(root)
	if err != nil {
		t.Fatal(err)
	}
	progress := 0
	deps := dependenciesFor(contents, nil)
	record, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1<<20, func(string, string, int64, int64) { progress++ }, deps)
	if err != nil {
		t.Fatal(err)
	}
	if progress == 0 || record.Validation != "isolated-import-and-converter-help-probe-passed" || len(record.PythonSHA256) != 64 {
		t.Fatalf("unexpected record: %#v", record)
	}
	again, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1<<20, nil, Dependencies{Download: func(context.Context, string, int64, int64, io.Writer, toolchain.ProgressFunc) error {
		return errors.New("downloaded twice")
	}, Install: deps.Install, Probe: deps.Probe})
	if err != nil || again.ID != record.ID {
		t.Fatalf("idempotent install failed: %#v %v", again, err)
	}
	records, err := List(root)
	if err != nil || len(records) != 1 {
		t.Fatalf("unexpected list: %#v %v", records, err)
	}
	if err := os.WriteFile(record.Converter, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1<<20, nil, deps); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("tamper accepted: %v", err)
	}
	removed, err := Remove(root, record.ID)
	if err != nil || removed.ID != record.ID {
		t.Fatalf("remove failed: %#v %v", removed, err)
	}
}

func TestConsentCancellationAndInstallFailureCleanUp(t *testing.T) {
	contents, _ := useFakeCatalog(t)
	root := t.TempDir()
	plan, _ := BuildPlan(root)
	changed := plan
	changed.PythonVersion = "different"
	if _, err := Execute(context.Background(), changed, plan.ConsentDigest, root, 1<<20, nil, dependenciesFor(contents, nil)); err == nil {
		t.Fatal("modified plan accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Execute(ctx, plan, plan.ConsentDigest, root, 1<<20, nil, dependenciesFor(contents, nil)); err == nil {
		t.Fatal("cancelled install succeeded")
	}
	if _, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1<<20, nil, dependenciesFor(contents, errors.New("install failed"))); err == nil {
		t.Fatal("failed dependency install succeeded")
	}
	if _, err := os.Stat(plan.Destination); !os.IsNotExist(err) {
		t.Fatalf("failed destination exists: %v", err)
	}
	parts, _ := filepath.Glob(filepath.Join(root, "downloads", "conversion-env-*.part"))
	if len(parts) != 0 {
		t.Fatalf("temporary files remain: %v", parts)
	}
}

func TestArchiveTraversalAndUnsafeSymlinkAreRejected(t *testing.T) {
	archive := zipBytes(t, map[string]string{"../escape": "bad"})
	filename := filepath.Join(t.TempDir(), "bad.zip")
	_ = os.WriteFile(filename, archive, 0o600)
	if err := extractArchive(filename, t.TempDir(), "zip"); err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("traversal accepted: %v", err)
	}
	root := t.TempDir()
	if err := safeSymlink(root, "python/bin/python", "../../../escape", filepath.Join(root, "python", "bin", "python")); err == nil {
		t.Fatal("escaping symlink accepted")
	}
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "python/bin/python", Typeflag: tar.TypeSymlink, Linkname: "../../../escape", Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(t.TempDir(), "bad.tar.gz")
	if err := os.WriteFile(tarPath, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(tarPath, t.TempDir(), "tar.gz"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("unsafe tar symlink accepted: %v", err)
	}
}

func TestPinnedCatalogAndLocks(t *testing.T) {
	for _, platform := range platforms {
		_, hash, packages, err := lockFor(platform)
		if err != nil || len(hash) != 64 || len(packages) < 20 {
			t.Fatalf("invalid lock for %s/%s: %s %d %v", platform.OS, platform.Arch, hash, len(packages), err)
		}
		for _, artifact := range []Artifact{platform.UV, platform.Python, sourceArtifact} {
			if len(artifact.SHA256) != 64 || artifact.SizeBytes <= 0 || !validateHTTPS(artifact.URL) {
				t.Fatalf("invalid artifact: %#v", artifact)
			}
		}
	}
	if _, err := platformFor("linux", "arm64"); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}
