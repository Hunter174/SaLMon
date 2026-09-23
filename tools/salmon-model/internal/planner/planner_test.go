package planner

import (
	"testing"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
)

func TestDirectGGUFPlanUsesResolvedFileIdentity(t *testing.T) {
	model := hub.Model{
		ID: "owner/model", SHA: "commit", PipelineTag: "text-generation", Tags: []string{"license:apache-2.0"},
		GGUF:     &hub.GGUFMetadata{Architecture: "qwen3"},
		Siblings: []hub.File{{Name: "model-Q4_K_M.gguf", Size: 100, LFS: &hub.LFSInfo{SHA256: "abcd", Size: 100}}},
	}
	plan := Build(model)
	if plan.Classification != "direct_gguf_candidate" || plan.ResolvedSHA != "commit" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if len(plan.GGUFFiles) != 1 || plan.GGUFFiles[0].Quantization != "Q4_K_M" || plan.GGUFFiles[0].SHA256 != "abcd" {
		t.Fatalf("unexpected GGUF: %#v", plan.GGUFFiles)
	}
}

func TestSourceModelIsOnlyUnverifiedCandidate(t *testing.T) {
	model := hub.Model{SHA: "commit", Config: hub.ModelConfig{ModelType: "bert"}, Siblings: []hub.File{{Name: "model.safetensors"}}}
	plan := Build(model)
	if plan.Classification != "unverified_conversion_candidate" || plan.SourceFormat != "safetensors" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestMissingMetadataDoesNotClaimConversion(t *testing.T) {
	plan := Build(hub.Model{SHA: "commit", Siblings: []hub.File{{Name: "weights.bin"}}})
	if plan.Classification != "unknown_or_unsupported" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}
