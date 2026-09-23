package planner

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
)

type Plan struct {
	Classification string     `json:"classification"`
	Explanation    string     `json:"explanation"`
	ResolvedSHA    string     `json:"resolved_sha"`
	License        string     `json:"license"`
	Architecture   string     `json:"architecture,omitempty"`
	Purposes       []string   `json:"candidate_purposes"`
	GGUFFiles      []GGUFFile `json:"gguf_files"`
	SourceFormat   string     `json:"source_format,omitempty"`
	Actions        []string   `json:"available_actions"`
	Warnings       []string   `json:"warnings"`
}

type GGUFFile struct {
	Name         string `json:"name"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	Quantization string `json:"quantization,omitempty"`
	MemoryNote   string `json:"memory_note"`
}

var quantizationPattern = regexp.MustCompile(`(?i)(Q[2-8](?:_[A-Z0-9]+)+|F16|FP16|F32|BF16)`)

func Build(model hub.Model) Plan {
	plan := Plan{
		ResolvedSHA:  model.SHA,
		License:      license(model),
		Architecture: architecture(model),
		Purposes:     purposes(model.PipelineTag),
		GGUFFiles:    []GGUFFile{}, Actions: []string{}, Warnings: []string{},
	}
	for _, file := range model.Siblings {
		if !strings.EqualFold(filepath.Ext(file.Name), ".gguf") {
			continue
		}
		size := file.Size
		if size == 0 && file.LFS != nil {
			size = file.LFS.Size
		}
		plan.GGUFFiles = append(plan.GGUFFiles, GGUFFile{
			Name: file.Name, SizeBytes: size, SHA256: file.ContentSHA256(),
			Quantization: inferQuantization(file.Name), MemoryNote: memoryNote(size),
		})
	}
	sort.Slice(plan.GGUFFiles, func(i, j int) bool { return plan.GGUFFiles[i].SizeBytes < plan.GGUFFiles[j].SizeBytes })
	if len(plan.GGUFFiles) > 0 {
		plan.Classification = "direct_gguf_candidate"
		plan.Explanation = "The repository contains GGUF files that can be downloaded directly, but runtime compatibility must still be validated locally."
		plan.Actions = []string{"inspect_gguf", "install_gguf", "import_local_gguf"}
		for _, file := range plan.GGUFFiles {
			if file.SHA256 == "" {
				plan.Warnings = append(plan.Warnings, "Hugging Face did not report a content SHA-256 for "+file.Name)
			}
		}
	} else {
		plan.SourceFormat = sourceFormat(model.Siblings)
		if plan.SourceFormat != "" && plan.Architecture != "" {
			plan.Classification = "unverified_conversion_candidate"
			plan.Explanation = "No GGUF is available. Source weights and architecture metadata exist, but compatibility with the pinned converter has not yet been proven."
			plan.Actions = []string{"find_gguf_variant", "check_conversion_support", "import_local_gguf"}
		} else {
			plan.Classification = "unknown_or_unsupported"
			plan.Explanation = "No directly usable GGUF was found and the repository does not expose enough metadata to propose conversion safely."
			plan.Actions = []string{"find_gguf_variant", "import_local_gguf"}
		}
	}
	if plan.License == "unknown" {
		plan.Warnings = append(plan.Warnings, "License metadata is missing; review the repository before downloading or converting.")
	}
	plan.Warnings = append(plan.Warnings, "Live Hugging Face metadata is unverified; this is not a SaLMon compatibility certification.")
	return plan
}

func license(model hub.Model) string {
	if model.CardData.License != "" {
		return model.CardData.License
	}
	for _, tag := range model.Tags {
		if strings.HasPrefix(tag, "license:") {
			return strings.TrimPrefix(tag, "license:")
		}
	}
	return "unknown"
}

func architecture(model hub.Model) string {
	if model.GGUF != nil && model.GGUF.Architecture != "" {
		return model.GGUF.Architecture
	}
	if model.Config.ModelType != "" {
		return model.Config.ModelType
	}
	if len(model.Config.Architectures) > 0 {
		return model.Config.Architectures[0]
	}
	return ""
}

func purposes(pipeline string) []string {
	switch pipeline {
	case "feature-extraction", "sentence-similarity", "text-embeddings-inference":
		return []string{"embedding"}
	case "text-generation", "text2text-generation":
		return []string{"chat", "decision"}
	default:
		return []string{"unknown"}
	}
}

func sourceFormat(files []hub.File) string {
	hasSafeTensors, hasPyTorch := false, false
	for _, file := range files {
		name := strings.ToLower(file.Name)
		hasSafeTensors = hasSafeTensors || strings.HasSuffix(name, ".safetensors")
		hasPyTorch = hasPyTorch || strings.HasSuffix(name, ".bin") || strings.HasSuffix(name, ".pth")
	}
	if hasSafeTensors {
		return "safetensors"
	}
	if hasPyTorch {
		return "pytorch"
	}
	return ""
}

func inferQuantization(filename string) string {
	match := quantizationPattern.FindString(strings.ToUpper(filename))
	return match
}

func memoryNote(size int64) string {
	if size <= 0 {
		return "File size unavailable; memory requirements cannot be estimated."
	}
	return "Model weights require at least the file size in memory plus context and runtime overhead; benchmark on target hardware."
}
