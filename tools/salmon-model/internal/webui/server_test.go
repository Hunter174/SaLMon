package webui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
)

func TestUIFlowAndStateChangingAuthorization(t *testing.T) {
	content := make([]byte, 64)
	copy(content, "GGUF")
	binary.LittleEndian.PutUint32(content[4:8], 3)
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	model := map[string]any{
		"id": "owner/model", "sha": "0123456789abcdef", "pipeline_tag": "text-generation",
		"tags":     []string{"license:apache-2.0"},
		"siblings": []any{map[string]any{"rfilename": "model-Q4_K_M.gguf", "size": len(content), "lfs": map[string]any{"sha256": hash, "size": len(content)}}},
	}
	hubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/models":
			_ = json.NewEncoder(w).Encode([]any{model})
		case strings.HasPrefix(r.URL.Path, "/api/models/owner/model/revision/"):
			_ = json.NewEncoder(w).Encode(model)
		case r.URL.Path == "/owner/model/resolve/0123456789abcdef/model-Q4_K_M.gguf":
			w.Header().Set("Content-Length", "64")
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hubServer.Close()
	client, err := hub.New(hubServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &Server{client: client, token: "test-token", root: t.TempDir(), ctx: ctx, plans: map[string]storedPlan{}, jobs: map[string]*Job{}, cancel: cancel}
	uiServer := httptest.NewServer(server.routes())
	defer uiServer.Close()
	server.origin = uiServer.URL

	response, err := http.Get(uiServer.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !bytes.Contains(page, []byte("test-token")) || response.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("index did not contain the protected session bootstrap")
	}

	unauthorized, _ := http.Post(uiServer.URL+"/api/install-plan", "application/json", strings.NewReader(`{}`))
	if unauthorized.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthorized mutation returned %d", unauthorized.StatusCode)
	}
	unauthorized.Body.Close()

	searchResponse, err := http.Get(uiServer.URL + "/api/search?q=test&format=gguf&limit=5")
	if err != nil || searchResponse.StatusCode != http.StatusOK {
		t.Fatalf("search failed: %v, %v", searchResponse.StatusCode, err)
	}
	searchResponse.Body.Close()

	recommendationResponse, err := http.Get(uiServer.URL + "/api/recommendations")
	if err != nil || recommendationResponse.StatusCode != http.StatusOK {
		t.Fatalf("recommendations failed: %v", err)
	}
	var recommendations struct{ Providers, Models []any }
	if err := json.NewDecoder(recommendationResponse.Body).Decode(&recommendations); err != nil {
		t.Fatal(err)
	}
	recommendationResponse.Body.Close()
	if len(recommendations.Providers) == 0 || len(recommendations.Models) == 0 {
		t.Fatal("reviewed recommendations are empty")
	}
	providerResponse, err := http.Get(uiServer.URL + "/api/providers/unsloth/models?limit=5")
	if err != nil || providerResponse.StatusCode != http.StatusOK {
		t.Fatalf("provider search failed: %v", err)
	}
	providerResponse.Body.Close()

	planBody := `{"repository":"owner/model","revision":"main","filename":"model-Q4_K_M.gguf"}`
	planResponse := authorizedRequest(t, uiServer.URL+"/api/install-plan", http.MethodPost, planBody, server)
	var plan struct {
		ConsentDigest string `json:"consent_digest"`
	}
	if err := json.NewDecoder(planResponse.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	planResponse.Body.Close()
	if len(plan.ConsentDigest) != 64 {
		t.Fatalf("invalid consent digest %q", plan.ConsentDigest)
	}

	installBody := `{"repository":"owner/model","revision":"0123456789abcdef","filename":"model-Q4_K_M.gguf","consent_digest":"` + plan.ConsentDigest + `"}`
	installResponse := authorizedRequest(t, uiServer.URL+"/api/install", http.MethodPost, installBody, server)
	var job Job
	if err := json.NewDecoder(installResponse.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	installResponse.Body.Close()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		response, err := http.Get(uiServer.URL + "/api/jobs/" + job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewDecoder(response.Body).Decode(&job); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if job.Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.Status != "completed" || job.Record == nil || job.Record.SHA256 != hash {
		t.Fatalf("installation job failed: %#v", job)
	}

	manifestPath := filepath.Join(t.TempDir(), "salmon.models.json")
	assignment, _ := json.Marshal(map[string]any{
		"manifest_path": manifestPath, "installation_id": job.Record.ID,
		"purposes": []string{"chat", "decision"}, "export_path": "models/dialogue.gguf",
	})
	assignmentResponse := authorizedRequest(t, uiServer.URL+"/api/project/assign", http.MethodPost, string(assignment), server)
	assignmentResponse.Body.Close()
	manifestResponse, err := http.Get(uiServer.URL + "/api/project?path=" + url.QueryEscape(manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Models []any `json:"models"`
	}
	if err := json.NewDecoder(manifestResponse.Body).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	manifestResponse.Body.Close()
	if len(manifest.Models) != 1 {
		t.Fatalf("project assignment was not saved: %#v", manifest)
	}
}

func authorizedRequest(t *testing.T, address, method, body string, server *Server) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, address, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.origin)
	request.Header.Set("X-Salmon-Token", server.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("request returned %d: %s", response.StatusCode, message)
	}
	return response
}
