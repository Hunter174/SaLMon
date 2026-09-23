package recommend

// This file curates source identities and a deliberately small set of models
// that SaLMon has actually exercised. It is not a mirror of Hugging Face model
// listings: provider inventories are always fetched live from the Hub.

type Provider struct {
	ID         string   `json:"id"`
	Account    string   `json:"account"`
	Name       string   `json:"name"`
	Category   string   `json:"category"`
	Purposes   []string `json:"purposes"`
	Rationale  string   `json:"rationale"`
	Caveat     string   `json:"caveat"`
	URL        string   `json:"url"`
	ReviewedAt string   `json:"reviewed_at"`
}

type File struct {
	Name         string `json:"name"`
	Quantization string `json:"quantization"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	Guidance     string `json:"guidance"`
}

type Model struct {
	ID               string   `json:"id"`
	Repository       string   `json:"repository"`
	ResolvedSHA      string   `json:"resolved_sha"`
	Name             string   `json:"name"`
	Purposes         []string `json:"purposes"`
	License          string   `json:"license"`
	Summary          string   `json:"summary"`
	ValidationStatus string   `json:"validation_status"`
	ValidationNote   string   `json:"validation_note"`
	Files            []File   `json:"files"`
	ReviewedAt       string   `json:"reviewed_at"`
}

func Providers() []Provider {
	return []Provider{
		{ID: "qwen", Account: "Qwen", Name: "Qwen", Category: "Upstream model publisher", Purposes: []string{"chat", "decision"}, Rationale: "Official Qwen account with first-party GGUF releases, model cards, usage guidance, and base-model provenance.", Caveat: "Official publication does not guarantee suitability for a particular game or commercial license obligations.", URL: "https://huggingface.co/Qwen", ReviewedAt: "2026-09-23"},
		{ID: "nomic", Account: "nomic-ai", Name: "Nomic AI", Category: "Upstream embedding publisher", Purposes: []string{"embedding"}, Rationale: "Publishes first-party llama.cpp-compatible embedding GGUFs with task prefixes, quantization details, and evaluation information.", Caveat: "Embedding architectures and required prefixes must still be validated against SaLMon before production use.", URL: "https://huggingface.co/nomic-ai", ReviewedAt: "2026-09-23"},
		{ID: "ggml", Account: "ggml-org", Name: "ggml-org", Category: "Runtime ecosystem", Purposes: []string{"chat", "decision", "embedding"}, Rationale: "The llama.cpp/GGUF project organization publishes reference conversions, compatibility models, and selected GGUF releases.", Caveat: "Some repositories are tests, experimental conversions, or automatic conversions rather than production recommendations.", URL: "https://huggingface.co/ggml-org", ReviewedAt: "2026-09-23"},
		{ID: "unsloth", Account: "unsloth", Name: "Unsloth", Category: "Established quantization publisher", Purposes: []string{"chat", "decision"}, Rationale: "Publishes broad GGUF coverage, dynamic quantizations, base-model links, usage notes, and multiple practical size/quality choices.", Caveat: "Dynamic and experimental quantizations need task-specific evaluation; inherited base-model terms still apply.", URL: "https://huggingface.co/unsloth", ReviewedAt: "2026-09-23"},
		{ID: "bartowski", Account: "bartowski", Name: "bartowski", Category: "Established quantization publisher", Purposes: []string{"chat", "decision"}, Rationale: "Publishes transparent llama.cpp/imatrix quantizations with conversion revision, source model, prompt format, and detailed quant guidance.", Caveat: "The account quantizes many third-party models; source-model quality and licensing vary substantially.", URL: "https://huggingface.co/bartowski", ReviewedAt: "2026-09-23"},
		{ID: "lmstudio", Account: "lmstudio-community", Name: "LM Studio Community", Category: "Selected community publisher", Purposes: []string{"chat", "decision"}, Rationale: "Highlights selected community models and exposes a smaller set of common GGUF quantizations with clear original-model attribution.", Caveat: "These remain third-party community models and carry explicit no-endorsement disclaimers.", URL: "https://huggingface.co/lmstudio-community", ReviewedAt: "2026-09-23"},
		{ID: "second-state", Account: "second-state", Name: "Second State", Category: "Embedding ecosystem publisher", Purposes: []string{"embedding"}, Rationale: "Publishes llama.cpp/LlamaEdge embedding GGUFs with quantization tables and links to original embedding models.", Caveat: "Only the listed MiniLM artifact has been checked by SaLMon; other repositories are unvalidated candidates.", URL: "https://huggingface.co/second-state", ReviewedAt: "2026-09-23"},
	}
}

func Models() []Model {
	return []Model{
		{
			ID: "qwen3-0.6b", Repository: "unsloth/Qwen3-0.6B-GGUF", ResolvedSHA: "50968a4468ef4233ed78cd7c3de230dd1d61a56b",
			Name: "Qwen3 0.6B", Purposes: []string{"chat", "decision"}, License: "apache-2.0",
			Summary:          "Small causal model used by SaLMon for local chat and direct-logit decision testing.",
			ValidationStatus: "mechanically_validated", ValidationNote: "Loads and runs locally in SaLMon. Q8 scored better than Q4 in the current semantic fixtures, but semantic quality and option-order robustness are not certified.",
			Files: []File{
				{Name: "Qwen3-0.6B-Q4_K_M.gguf", Quantization: "Q4_K_M", SizeBytes: 396705472, SHA256: "ac2d97712095a558e31573f62f466a3f9d93990898b0ec79d7c974c1780d524a", Guidance: "Smaller deployment candidate."},
				{Name: "Qwen3-0.6B-Q8_0.gguf", Quantization: "Q8_0", SizeBytes: 639447744, SHA256: "e150ed544dfe6016930c026a93913a5e3184181ebfe6ab2223ae01dd0491784c", Guidance: "Prefer for current semantic quality experiments when the larger file is acceptable."},
			}, ReviewedAt: "2026-09-23",
		},
		{
			ID: "minilm-l6-v2", Repository: "second-state/All-MiniLM-L6-v2-Embedding-GGUF", ResolvedSHA: "544f204f2eaa2d71361ffc74d6df7170285b286a",
			Name: "All-MiniLM-L6-v2", Purposes: []string{"embedding"}, License: "apache-2.0",
			Summary:          "Compact English sentence embedding model for lore and NPC-memory retrieval.",
			ValidationStatus: "reference_validated", ValidationNote: "Q4 embedding geometry passed SaLMon's canonical FP32 reference comparison with maximum cosine delta 0.021439 (tolerance 0.08).",
			Files: []File{
				{Name: "all-MiniLM-L6-v2-Q4_K_M.gguf", Quantization: "Q4_K_M", SizeBytes: 20999104, SHA256: "2ec4cee28a27a9c973d5f5230930d6ef6e52694bd2bc71be26a9bef5b1d755e6", Guidance: "Validated compact retrieval default."},
			}, ReviewedAt: "2026-09-23",
		},
	}
}

func ProviderByID(id string) (Provider, bool) {
	for _, provider := range Providers() {
		if provider.ID == id {
			return provider, true
		}
	}
	return Provider{}, false
}
