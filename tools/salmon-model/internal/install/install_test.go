package install

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
)

func ggufBytes() []byte {
	content := make([]byte, 64)
	copy(content, "GGUF")
	binary.LittleEndian.PutUint32(content[4:8], 3)
	copy(content[8:], "test model content")
	return content
}

func modelFor(content []byte) hub.Model {
	hash := sha256.Sum256(content)
	return hub.Model{
		ID: "owner/model", SHA: "0123456789abcdef", PipelineTag: "text-generation",
		Tags: []string{"license:apache-2.0"},
		Siblings: []hub.File{{Name: "model-Q4_K_M.gguf", Size: int64(len(content)), LFS: &hub.LFSInfo{
			SHA256: hex.EncodeToString(hash[:]), Size: int64(len(content)),
		}}},
	}
}

func TestVerifiedAtomicInstallAndList(t *testing.T) {
	content := ggufBytes()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/owner/model/resolve/0123456789abcdef/model-Q4_K_M.gguf" {
			t.Fatalf("unexpected download path %s", r.URL.Path)
		}
		w.Header().Set("Content-Length", "64")
		_, _ = w.Write(content)
	}))
	defer server.Close()
	client, err := hub.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	plan, err := BuildPlan(modelFor(content), "model-Q4_K_M.gguf", root)
	if err != nil {
		t.Fatal(err)
	}
	record, err := Execute(context.Background(), client, plan, plan.ConsentDigest, root, 1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	if record.RuntimeValidation != "not-run" {
		t.Fatalf("runtime validation was overstated: %#v", record)
	}
	installed, err := os.ReadFile(record.Path)
	if err != nil || string(installed) != string(content) {
		t.Fatalf("installed content mismatch: %v", err)
	}
	records, err := List(root)
	if err != nil || len(records) != 1 || records[0].ID != record.ID {
		t.Fatalf("unexpected registry: %#v, %v", records, err)
	}
	parts, _ := filepath.Glob(filepath.Join(root, "downloads", "*.part"))
	if len(parts) != 0 {
		t.Fatalf("temporary downloads remain: %v", parts)
	}
	removed, err := Remove(root, record.ID)
	if err != nil || removed.ID != record.ID {
		t.Fatalf("remove failed: %#v, %v", removed, err)
	}
	if _, err := os.Stat(record.Path); !os.IsNotExist(err) {
		t.Fatalf("model content still exists after remove: %v", err)
	}
	records, err = List(root)
	if err != nil || len(records) != 0 {
		t.Fatalf("registry not empty after remove: %#v, %v", records, err)
	}
}

func TestCancellationRemovesPartialDownload(t *testing.T) {
	content := ggufBytes()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(content) }))
	defer server.Close()
	client, _ := hub.New(server.URL)
	root := t.TempDir()
	plan, err := BuildPlan(modelFor(content), "model-Q4_K_M.gguf", root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Execute(ctx, client, plan, plan.ConsentDigest, root, 1024, nil); err == nil {
		t.Fatal("cancelled installation succeeded")
	}
	parts, _ := filepath.Glob(filepath.Join(root, "downloads", "*.part"))
	if len(parts) != 0 {
		t.Fatalf("cancelled download was retained: %v", parts)
	}
}

func TestConsentIsBoundToExactPlan(t *testing.T) {
	content := ggufBytes()
	client, _ := hub.New("http://127.0.0.1:1")
	root := t.TempDir()
	plan, err := BuildPlan(modelFor(content), "model-Q4_K_M.gguf", root)
	if err != nil {
		t.Fatal(err)
	}
	plan.Filename = "different.gguf"
	if _, err := Execute(context.Background(), client, plan, plan.ConsentDigest, root, 1024, nil); err == nil {
		t.Fatal("modified plan accepted old consent")
	}
}

func TestRejectsNonGGUFContentAndCleansTemporaryFile(t *testing.T) {
	content := ggufBytes()
	bad := append([]byte(nil), content...)
	copy(bad, "NOPE")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(bad) }))
	defer server.Close()
	client, _ := hub.New(server.URL)
	root := t.TempDir()
	model := modelFor(bad)
	plan, err := BuildPlan(model, "model-Q4_K_M.gguf", root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(context.Background(), client, plan, plan.ConsentDigest, root, 1024, nil); err == nil {
		t.Fatal("non-GGUF content installed")
	}
	parts, _ := filepath.Glob(filepath.Join(root, "downloads", "*.part"))
	if len(parts) != 0 {
		t.Fatalf("failed download was retained: %v", parts)
	}
}
