package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchIsLiveAndNormalized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("filter") != "gguf" || r.URL.Query().Get("full") != "true" || r.URL.Query().Get("author") != "owner" {
			t.Fatalf("missing search controls: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"owner/model","sha":"abc","siblings":[{"rfilename":"model.Q4_K_M.gguf"}]}]`))
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.Search(context.Background(), SearchOptions{Query: "small model", Format: "gguf", Limit: 5, Author: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "owner/model" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestInspectReadsBlobIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models/owner/model/revision/main" || r.URL.Query().Get("blobs") != "true" {
			t.Fatalf("unexpected inspect URL %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":"owner/model","sha":"commit","siblings":[{"rfilename":"m.gguf","size":12,"lfs":{"sha256":"AABB","size":12}}]}`))
	}))
	defer server.Close()
	client, _ := New(server.URL)
	model, err := client.Inspect(context.Background(), "owner/model", "main")
	if err != nil {
		t.Fatal(err)
	}
	if model.SHA != "commit" || model.Siblings[0].ContentSHA256() != "aabb" {
		t.Fatalf("unexpected model: %#v", model)
	}
}

func TestRejectsUnsafeInputs(t *testing.T) {
	client, _ := New("https://huggingface.co")
	if _, err := client.Inspect(context.Background(), "../model", "main"); err == nil {
		t.Fatal("unsafe repository accepted")
	}
	if _, err := client.Search(context.Background(), SearchOptions{Query: strings.Repeat("x", 201), Format: "any", Limit: 1}); err == nil {
		t.Fatal("oversized query accepted")
	}
	if _, err := client.Search(context.Background(), SearchOptions{Query: "test", Format: "gguf", Limit: 1, Author: "../owner"}); err == nil {
		t.Fatal("unsafe author accepted")
	}
}

func TestRequiresHTTPSOutsideLoopback(t *testing.T) {
	if _, err := New("http://example.com"); err == nil {
		t.Fatal("insecure endpoint accepted")
	}
}
