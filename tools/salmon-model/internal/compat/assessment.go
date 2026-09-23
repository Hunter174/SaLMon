package compat

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
)

type ConverterCapability struct {
	Text      bool
	Projector bool
}

type Action struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Available   bool   `json:"available"`
	Explanation string `json:"explanation"`
}

type Estimate struct {
	Preset          string `json:"preset"`
	Minimum         int64  `json:"minimum_bytes"`
	Maximum         int64  `json:"maximum_bytes"`
	PeakDiskMinimum int64  `json:"peak_disk_minimum_bytes"`
	PeakDiskMaximum int64  `json:"peak_disk_maximum_bytes"`
	Warning         string `json:"warning"`
}

type SourceFile struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type Assessment struct {
	Status               string       `json:"status"`
	Explanation          string       `json:"explanation"`
	Architectures        []string     `json:"architectures"`
	SourceFormat         string       `json:"source_format,omitempty"`
	SourceWeightBytes    int64        `json:"source_weight_bytes,omitempty"`
	ConverterLlamaCommit string       `json:"converter_llama_commit"`
	MissingRequirements  []string     `json:"missing_requirements"`
	RequiredSourceFiles  []SourceFile `json:"required_source_files"`
	Warnings             []string     `json:"warnings"`
	OutputEstimates      []Estimate   `json:"output_estimates"`
	Actions              []Action     `json:"actions"`
}

func Assess(model hub.Model) Assessment {
	assessment := Assessment{
		Architectures:        append([]string(nil), model.Config.Architectures...),
		ConverterLlamaCommit: ConverterLlamaCommit,
		MissingRequirements:  []string{}, RequiredSourceFiles: []SourceFile{}, Warnings: []string{}, OutputEstimates: []Estimate{},
	}
	hasGGUF := false
	hasConfig, hasTokenizer := false, false
	hasSafeTensors, hasPyTorch := false, false
	allSourceWeightsHashed := true
	for _, file := range model.Siblings {
		name := strings.ToLower(file.Name)
		size := file.Size
		if size == 0 && file.LFS != nil {
			size = file.LFS.Size
		}
		switch {
		case strings.HasSuffix(name, ".gguf"):
			hasGGUF = true
		case name == "config.json":
			hasConfig = true
			assessment.RequiredSourceFiles = append(assessment.RequiredSourceFiles, SourceFile{Name: file.Name, Role: "configuration", SizeBytes: size})
		case isTokenizerFile(name):
			hasTokenizer = hasTokenizer || isPrimaryTokenizerFile(name)
			assessment.RequiredSourceFiles = append(assessment.RequiredSourceFiles, SourceFile{Name: file.Name, Role: "tokenizer", SizeBytes: size})
		case strings.HasSuffix(name, ".safetensors.index.json"):
			assessment.RequiredSourceFiles = append(assessment.RequiredSourceFiles, SourceFile{Name: file.Name, Role: "weight index", SizeBytes: size})
		case strings.HasSuffix(name, ".safetensors"):
			hasSafeTensors = true
			assessment.SourceWeightBytes += size
			assessment.RequiredSourceFiles = append(assessment.RequiredSourceFiles, SourceFile{Name: file.Name, Role: "weights", SizeBytes: size, SHA256: file.ContentSHA256()})
			if file.ContentSHA256() == "" {
				allSourceWeightsHashed = false
			}
		case strings.HasSuffix(name, ".bin") || strings.HasSuffix(name, ".pth"):
			hasPyTorch = true
			assessment.SourceWeightBytes += size
			assessment.RequiredSourceFiles = append(assessment.RequiredSourceFiles, SourceFile{Name: file.Name, Role: "weights", SizeBytes: size, SHA256: file.ContentSHA256()})
			if file.ContentSHA256() == "" {
				allSourceWeightsHashed = false
			}
		}
	}
	assessment.Actions = []Action{
		{ID: "find_gguf", Label: "Find a GGUF variant", Available: true, Explanation: "Search Hugging Face for a repository that already provides GGUF files."},
		{ID: "import_gguf", Label: "Import a local GGUF", Available: true, Explanation: "Use an existing local GGUF file instead of preparing this repository."},
	}
	if hasGGUF {
		assessment.Status = "direct_gguf_available"
		assessment.Explanation = "This repository already contains GGUF files; conversion is unnecessary."
		return assessment
	}
	if hasSafeTensors {
		assessment.SourceFormat = "safetensors"
	} else if hasPyTorch {
		assessment.SourceFormat = "pytorch"
	}
	if !hasConfig {
		assessment.MissingRequirements = append(assessment.MissingRequirements, "config.json")
	}
	if !hasTokenizer {
		assessment.MissingRequirements = append(assessment.MissingRequirements, "tokenizer assets")
	}
	if !hasSafeTensors && !hasPyTorch {
		assessment.MissingRequirements = append(assessment.MissingRequirements, "supported source weights")
	}
	if (hasSafeTensors || hasPyTorch) && !allSourceWeightsHashed {
		assessment.MissingRequirements = append(assessment.MissingRequirements, "SHA-256 metadata for every source-weight file")
	}
	if hasPyTorch && !hasSafeTensors {
		assessment.Warnings = append(assessment.Warnings, "PyTorch pickle weights require a stricter isolated preparation policy; Safetensors is preferred.")
	}
	textSupported, projectorSupported := false, false
	for _, architecture := range assessment.Architectures {
		if capability, found := converterArchitectures[architecture]; found {
			textSupported = textSupported || capability.Text
			projectorSupported = projectorSupported || capability.Projector
		}
	}
	if projectorSupported {
		assessment.Warnings = append(assessment.Warnings, "The pinned converter recognizes a multimodal projector, but SaLMon's current runtime API is text and embeddings only.")
	}
	convertAction := Action{ID: "prepare", Label: "Convert and quantize", Available: false}
	switch {
	case len(assessment.Architectures) == 0:
		assessment.Status = "unknown_architecture"
		assessment.Explanation = "The repository does not declare a Transformers architecture, so converter support cannot be established."
		convertAction.Explanation = "A declared config.architectures value is required before preparation can be offered."
	case !textSupported:
		assessment.Status = "unsupported_architecture"
		assessment.Explanation = "None of the declared architectures are registered by SaLMon's pinned llama.cpp converter."
		convertAction.Explanation = "The pinned converter does not support this architecture."
	case len(assessment.MissingRequirements) > 0:
		assessment.Status = "incomplete_conversion_source"
		assessment.Explanation = "The architecture is recognized, but files or immutable metadata required for safe preparation are missing."
		convertAction.Explanation = "Resolve the listed missing requirements before preparation."
	default:
		assessment.Status = "conversion_toolchain_required"
		assessment.Explanation = "The architecture and Safetensors source layout appear compatible with the pinned converter, but the optional preparation toolchain is not installed yet."
		if hasPyTorch && !hasSafeTensors {
			assessment.Explanation = "The architecture is recognized, but PyTorch-only preparation remains disabled pending the isolated toolchain policy."
		}
		convertAction.Explanation = "Install and consent to the optional pinned toolchain from issue #18 when it becomes available."
		assessment.OutputEstimates = estimates(assessment.SourceWeightBytes)
	}
	assessment.Actions = append(assessment.Actions, convertAction)
	sort.Strings(assessment.MissingRequirements)
	return assessment
}

func estimates(sourceBytes int64) []Estimate {
	if sourceBytes <= 0 {
		return []Estimate{}
	}
	warning := "Approximation from source-weight bytes only; tensor types, architecture, metadata, and quantization can change final size and peak workspace."
	f16Min, f16Max := sourceBytes*8/10, sourceBytes*12/10
	makeEstimate := func(preset string, minimum, maximum int64, intermediate bool) Estimate {
		peakMaximum := sourceBytes + maximum
		if intermediate {
			peakMaximum += f16Max
		}
		return Estimate{Preset: preset, Minimum: minimum, Maximum: maximum, PeakDiskMinimum: sourceBytes + minimum, PeakDiskMaximum: peakMaximum, Warning: warning}
	}
	return []Estimate{
		makeEstimate("F16", f16Min, f16Max, false),
		makeEstimate("Q8_0", sourceBytes*45/100, sourceBytes*65/100, true),
		makeEstimate("Q5_K_M", sourceBytes*28/100, sourceBytes*45/100, true),
		makeEstimate("Q4_K_M", sourceBytes*22/100, sourceBytes*38/100, true),
	}
}

func isTokenizerFile(name string) bool {
	switch name {
	case "tokenizer.json", "tokenizer.model", "spiece.model", "vocab.txt", "vocab.json", "merges.txt", "tokenizer_config.json", "special_tokens_map.json", "added_tokens.json":
		return true
	default:
		return false
	}
}

func isPrimaryTokenizerFile(name string) bool {
	switch name {
	case "tokenizer.json", "tokenizer.model", "spiece.model", "vocab.txt", "vocab.json":
		return true
	default:
		return false
	}
}

func IsSourceWeight(name string) bool {
	extension := strings.ToLower(filepath.Ext(name))
	return extension == ".safetensors" || extension == ".bin" || extension == ".pth"
}
