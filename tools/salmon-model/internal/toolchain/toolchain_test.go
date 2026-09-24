package toolchain

import (
	"archive/zip"
	"bytes"
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
)

func testArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range entries {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o755)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func useTestArtifact(t *testing.T, archive []byte) Artifact {
	t.Helper()
	original := append([]Artifact(nil), artifacts...)
	t.Cleanup(func() { artifacts = original })
	hash := sha256.Sum256(archive)
	for index := range artifacts {
		if artifacts[index].OS == runtime.GOOS && artifacts[index].Arch == runtime.GOARCH {
			artifacts[index].Filename = "test.zip"
			artifacts[index].SizeBytes = int64(len(archive))
			artifacts[index].SHA256 = hex.EncodeToString(hash[:])
			artifacts[index].URL = "https://example.invalid/test.zip"
			return artifacts[index]
		}
	}
	t.Fatalf("test platform %s/%s has no catalog artifact", runtime.GOOS, runtime.GOARCH)
	return Artifact{}
}

func dependencies(archive []byte, probeError error) Dependencies {
	return Dependencies{
		Download: func(ctx context.Context, _ string, expected, maximum int64, destination io.Writer, progress ProgressFunc) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if int64(len(archive)) != expected || expected > maximum {
				return errors.New("unexpected test download bounds")
			}
			if _, err := destination.Write(archive); err != nil {
				return err
			}
			if progress != nil {
				progress(expected, expected)
			}
			return nil
		},
		Probe: func(_ context.Context, binary string) error {
			if probeError != nil {
				return probeError
			}
			data, err := os.ReadFile(binary)
			if err != nil {
				return err
			}
			if string(data) != "fake quantizer" {
				return errors.New("unexpected fake executable")
			}
			return nil
		},
	}
}

func TestVerifiedAtomicInstallListIdempotenceAndRemove(t *testing.T) {
	binaryRelative := artifactsForCurrentTest(t).BinaryRelative
	archive := testArchive(t, map[string]string{binaryRelative: "fake quantizer", "NOTICE": "upstream license"})
	useTestArtifact(t, archive)
	root := t.TempDir()
	plan, err := BuildPlan(root)
	if err != nil {
		t.Fatal(err)
	}
	progressCalled := false
	record, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1<<20, func(done, total int64) {
		progressCalled = done == total
	}, dependencies(archive, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !progressCalled || record.Validation != "quantize-help-probe-passed" {
		t.Fatalf("unexpected record or progress: %#v", record)
	}
	if data, err := os.ReadFile(record.Binary); err != nil || string(data) != "fake quantizer" {
		t.Fatalf("installed executable mismatch: %q, %v", data, err)
	}
	again, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1<<20, nil, Dependencies{Probe: dependencies(archive, nil).Probe, Download: func(context.Context, string, int64, int64, io.Writer, ProgressFunc) error {
		return errors.New("idempotent install downloaded again")
	}})
	if err != nil || again.ID != record.ID {
		t.Fatalf("idempotent install failed: %#v, %v", again, err)
	}
	records, err := List(root)
	if err != nil || len(records) != 1 || records[0].ID != record.ID {
		t.Fatalf("unexpected records: %#v, %v", records, err)
	}
	if err := os.WriteFile(record.Binary, []byte("tampered quantizer"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1<<20, nil, dependencies(archive, nil)); err == nil || !strings.Contains(err.Error(), "executable hash") {
		t.Fatalf("tampered installed executable was accepted: %v", err)
	}
	removed, err := Remove(root, record.ID)
	if err != nil || removed.ID != record.ID {
		t.Fatalf("remove failed: %#v, %v", removed, err)
	}
	if _, err := os.Stat(record.Path); !os.IsNotExist(err) {
		t.Fatalf("toolchain remains after removal: %v", err)
	}
}

func TestConsentIsBoundToPinnedPlan(t *testing.T) {
	binaryRelative := artifactsForCurrentTest(t).BinaryRelative
	archive := testArchive(t, map[string]string{binaryRelative: "fake quantizer"})
	useTestArtifact(t, archive)
	root := t.TempDir()
	plan, err := BuildPlan(root)
	if err != nil {
		t.Fatal(err)
	}
	plan.Destination += "-changed"
	if _, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1<<20, nil, dependencies(archive, nil)); err == nil {
		t.Fatal("modified plan accepted old consent")
	}
}

func TestCancellationAndProbeFailureLeaveNoInstallation(t *testing.T) {
	binaryRelative := artifactsForCurrentTest(t).BinaryRelative
	archive := testArchive(t, map[string]string{binaryRelative: "fake quantizer"})
	useTestArtifact(t, archive)
	root := t.TempDir()
	plan, _ := BuildPlan(root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Execute(ctx, plan, plan.ConsentDigest, root, 1<<20, nil, dependencies(archive, nil)); err == nil {
		t.Fatal("cancelled install succeeded")
	}
	if _, err := os.Stat(plan.Destination); !os.IsNotExist(err) {
		t.Fatalf("cancelled destination exists: %v", err)
	}
	if _, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1<<20, nil, dependencies(archive, errors.New("probe rejected"))); err == nil {
		t.Fatal("probe failure installed toolchain")
	}
	if _, err := os.Stat(plan.Destination); !os.IsNotExist(err) {
		t.Fatalf("failed destination exists: %v", err)
	}
	parts, _ := filepath.Glob(filepath.Join(root, "downloads", "*.part"))
	if len(parts) != 0 {
		t.Fatalf("temporary downloads remain: %v", parts)
	}
}

func TestRejectsArchivePathTraversal(t *testing.T) {
	binaryRelative := artifactsForCurrentTest(t).BinaryRelative
	archive := testArchive(t, map[string]string{binaryRelative: "fake quantizer", "../escape": "bad"})
	useTestArtifact(t, archive)
	root := t.TempDir()
	plan, _ := BuildPlan(root)
	if _, err := Execute(context.Background(), plan, plan.ConsentDigest, root, 1<<20, nil, dependencies(archive, nil)); err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("unsafe archive was not rejected clearly: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("archive escaped staging directory: %v", err)
	}
}

func TestCatalogPinsSupportedPlatforms(t *testing.T) {
	for _, target := range [][2]string{{"windows", "amd64"}, {"linux", "amd64"}, {"darwin", "amd64"}, {"darwin", "arm64"}} {
		artifact, err := artifactFor(target[0], target[1])
		if err != nil || len(artifact.SHA256) != 64 || !strings.HasPrefix(artifact.URL, "https://github.com/ggml-org/llama.cpp/releases/download/b6002/") {
			t.Fatalf("invalid catalog entry for %v: %#v, %v", target, artifact, err)
		}
	}
	if _, err := artifactFor("linux", "arm64"); err == nil {
		t.Fatal("unsupported platform was accepted")
	}
}

func artifactsForCurrentTest(t *testing.T) Artifact {
	t.Helper()
	artifact, err := artifactFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}
