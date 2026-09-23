package project

import (
	"path/filepath"
	"testing"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/install"
)

func testRecord(id, filename, hash string) install.Record {
	return install.Record{
		ID: id, Repository: "owner/model", ResolvedSHA: "commit", Filename: filename,
		SHA256: hash, License: "apache-2.0", SourceURL: "https://huggingface.co/owner/model/tree/commit",
	}
}

func TestAssignCreatesPortableManifestAndReplacesExportDestination(t *testing.T) {
	filename := filepath.Join(t.TempDir(), Filename)
	first := testRecord("11111111111111111111111111111111", "chat.gguf", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	manifest, err := Assign(filename, first, []string{"decision", "chat", "chat"}, "models/dialogue.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Models) != 1 || manifest.Models[0].Purposes[0] != "chat" || manifest.Models[0].Purposes[1] != "decision" {
		t.Fatalf("unexpected assignment: %#v", manifest)
	}
	second := testRecord("22222222222222222222222222222222", "replacement.gguf", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	manifest, err = Assign(filename, second, []string{"chat"}, "models/dialogue.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Models) != 1 || manifest.Models[0].InstallationID != second.ID {
		t.Fatalf("destination was not replaced: %#v", manifest)
	}
	loaded, err := Load(filename)
	if err != nil || loaded.Models[0].InstallationID != second.ID {
		t.Fatalf("manifest did not persist: %#v, %v", loaded, err)
	}
	removed, err := Remove(filename, "models/dialogue.gguf")
	if err != nil || len(removed.Models) != 0 {
		t.Fatalf("assignment was not removed: %#v, %v", removed, err)
	}
}

func TestManifestRejectsUnsafePathsAndPurposes(t *testing.T) {
	record := testRecord("11111111111111111111111111111111", "chat.gguf", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if _, err := Assign(filepath.Join(t.TempDir(), "other.json"), record, []string{"chat"}, "models/chat.gguf"); err == nil {
		t.Fatal("accepted wrong manifest filename")
	}
	filename := filepath.Join(t.TempDir(), Filename)
	if _, err := Assign(filename, record, []string{"chat"}, "../chat.gguf"); err == nil {
		t.Fatal("accepted escaping export path")
	}
	if _, err := Assign(filename, record, []string{"image"}, "models/chat.gguf"); err == nil {
		t.Fatal("accepted unsupported purpose")
	}
}
