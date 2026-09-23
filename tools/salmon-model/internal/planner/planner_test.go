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

func TestRecognizedCompleteSourceRequiresOptionalToolchain(t *testing.T) {
	model := hub.Model{
		SHA: "commit", Config: hub.ModelConfig{ModelType: "qwen3", Architectures: []string{"Qwen3ForCausalLM"}},
		Siblings: []hub.File{
			{Name: "config.json"}, {Name: "tokenizer.json"},
			{Name: "model.safetensors", Size: 200, LFS: &hub.LFSInfo{SHA256: "abcd", Size: 200}},
		},
	}
	plan := Build(model)
	if plan.Classification != "conversion_toolchain_required" || plan.SourceFormat != "safetensors" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if len(plan.Preparation.OutputEstimates) == 0 || plan.Preparation.Actions[2].Available {
		t.Fatalf("expected estimates and a disabled preparation handoff: %#v", plan.Preparation)
	}
}

func TestMissingMetadataDoesNotClaimConversion(t *testing.T) {
	plan := Build(hub.Model{SHA: "commit", Siblings: []hub.File{{Name: "weights.bin"}}})
	if plan.Classification != "unknown_architecture" || len(plan.Preparation.MissingRequirements) == 0 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}
