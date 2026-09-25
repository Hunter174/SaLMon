package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
)

func preparationRequestForTest(t *testing.T, address, body string, server *Server) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, address, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", server.origin)
	request.Header.Set("X-Salmon-Token", server.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestPreparationPlanConsentAndToolchainHandoff(t *testing.T) {
	client, err := hub.New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &Server{client: client, root: t.TempDir(), token: "test-token", ctx: ctx, plans: map[string]storedPlan{}, jobs: map[string]*Job{}}
	ui := httptest.NewServer(server.routes())
	defer ui.Close()
	server.origin = ui.URL
	untrusted, _ := http.Post(ui.URL+"/api/preparation/conversion-toolchain/plan", "application/json", strings.NewReader("{}"))
	if untrusted.StatusCode != http.StatusForbidden {
		t.Fatalf("untrusted plan returned %d", untrusted.StatusCode)
	}
	untrusted.Body.Close()
	listed, err := http.Get(ui.URL + "/api/preparation/toolchains")
	if err != nil || listed.StatusCode != http.StatusOK {
		t.Fatalf("toolchain list: %v %v", listed.StatusCode, err)
	}
	listed.Body.Close()
	for _, kind := range []string{"conversion-toolchain", "quantizer-toolchain"} {
		response := authorizedRequest(t, ui.URL+"/api/preparation/"+kind+"/plan", http.MethodPost, "{}", server)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s plan returned %d", kind, response.StatusCode)
		}
		var plan struct {
			ConsentDigest string `json:"consent_digest"`
		}
		if err := json.NewDecoder(response.Body).Decode(&plan); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if len(plan.ConsentDigest) != 64 {
			t.Fatal("missing exact consent")
		}
		wrong := preparationRequestForTest(t, ui.URL+"/api/preparation/"+kind+"/run", `{"consent_digest":"wrong"}`, server)
		if wrong.StatusCode != http.StatusBadRequest {
			t.Fatalf("missing consent accepted: %d", wrong.StatusCode)
		}
		wrong.Body.Close()
		cross := "quantizer-toolchain"
		if kind == cross {
			cross = "conversion-toolchain"
		}
		wrongKind := preparationRequestForTest(t, ui.URL+"/api/preparation/"+cross+"/run", `{"consent_digest":"`+plan.ConsentDigest+`"}`, server)
		if wrongKind.StatusCode != http.StatusBadRequest {
			t.Fatalf("cross-operation consent accepted: %d", wrongKind.StatusCode)
		}
		wrongKind.Body.Close()
	}
	for _, kind := range []string{"convert", "quantize", "unknown"} {
		response := preparationRequestForTest(t, ui.URL+"/api/preparation/"+kind+"/plan", "{}", server)
		if response.StatusCode == http.StatusOK {
			t.Fatalf("incomplete %s plan accepted", kind)
		}
		response.Body.Close()
	}
}
