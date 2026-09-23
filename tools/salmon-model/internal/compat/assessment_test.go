package compat

import (
	"os"
	"regexp"
	"testing"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
)

func TestGeneratedMatrixMatchesPinnedConverterRegistrations(t *testing.T) {
	source, err := os.ReadFile("../../../../lib/llama.cpp/convert_hf_to_gguf.py")
	if err != nil {
		t.Skipf("pinned converter source unavailable: %v", err)
	}
	blocks := regexp.MustCompile(`(?s)@ModelBase\.register\((.*?)\)`).FindAllSubmatch(source, -1)
	quoted := regexp.MustCompile(`["']([^"']+)["']`)
	found := map[string]bool{}
	for _, block := range blocks {
		for _, match := range quoted.FindAllSubmatch(block[1], -1) {
			found[string(match[1])] = true
		}
	}
	if len(found) != len(converterArchitectures) {
		t.Fatalf("generated matrix has %d architectures; pinned converter has %d (run scripts/generate_converter_matrix.py)", len(converterArchitectures), len(found))
	}
	for architecture := range found {
		if _, ok := converterArchitectures[architecture]; !ok {
			t.Errorf("generated matrix is missing %q", architecture)
		}
	}
}

func TestUnsupportedAndIncompleteSourcesStayDisabled(t *testing.T) {
	unsupported := Assess(hub.Model{Config: hub.ModelConfig{Architectures: []string{"NovelForCausalLM"}}})
	if unsupported.Status != "unsupported_architecture" || unsupported.Actions[2].Available {
		t.Fatalf("unexpected unsupported assessment: %#v", unsupported)
	}
	incomplete := Assess(hub.Model{Config: hub.ModelConfig{Architectures: []string{"Qwen3ForCausalLM"}}})
	if incomplete.Status != "incomplete_conversion_source" || len(incomplete.MissingRequirements) != 3 {
		t.Fatalf("unexpected incomplete assessment: %#v", incomplete)
	}
}
