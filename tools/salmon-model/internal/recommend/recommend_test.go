package recommend

import "testing"

func TestRecommendationsHaveAuditableIdentity(t *testing.T) {
	providerIDs := map[string]bool{}
	accounts := map[string]bool{}
	for _, provider := range Providers() {
		if provider.ID == "" || provider.Account == "" || provider.Rationale == "" || provider.Caveat == "" || provider.ReviewedAt == "" {
			t.Fatalf("incomplete provider: %#v", provider)
		}
		if providerIDs[provider.ID] || accounts[provider.Account] {
			t.Fatalf("duplicate provider: %#v", provider)
		}
		providerIDs[provider.ID], accounts[provider.Account] = true, true
	}
	for _, model := range Models() {
		if len(model.ResolvedSHA) != 40 || model.ValidationStatus == "" || model.ValidationNote == "" {
			t.Fatalf("model recommendation is not auditable: %#v", model)
		}
		for _, file := range model.Files {
			if len(file.SHA256) != 64 || file.SizeBytes <= 0 {
				t.Fatalf("recommended file lacks immutable identity: %#v", file)
			}
		}
	}
}

func TestProviderLookupOnlyAcceptsReviewedIDs(t *testing.T) {
	if provider, ok := ProviderByID("unsloth"); !ok || provider.Account != "unsloth" {
		t.Fatal("reviewed provider was not found")
	}
	if _, ok := ProviderByID("unknown"); ok {
		t.Fatal("unknown provider was accepted")
	}
}
