package webui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/install"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/planner"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/project"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/recommend"
)

//go:embed static/*
var assets embed.FS

type Options struct {
	OpenBrowser bool
	Writer      *os.File
}

type Server struct {
	client *hub.Client
	token  string
	origin string
	root   string
	ctx    context.Context
	mu     sync.Mutex
	plans  map[string]storedPlan
	jobs   map[string]*Job
	cancel context.CancelFunc
}

type storedPlan struct {
	Plan      install.Plan
	ExpiresAt time.Time
}

type Job struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	CompletedBytes int64           `json:"completed_bytes"`
	TotalBytes     int64           `json:"total_bytes"`
	Record         *install.Record `json:"record,omitempty"`
	Error          string          `json:"error,omitempty"`
	cancel         context.CancelFunc
}

type installRequest struct {
	Repository    string `json:"repository"`
	Revision      string `json:"revision"`
	Filename      string `json:"filename"`
	ConsentDigest string `json:"consent_digest"`
}

type projectRequest struct {
	ManifestPath   string   `json:"manifest_path"`
	InstallationID string   `json:"installation_id"`
	Purposes       []string `json:"purposes"`
	ExportPath     string   `json:"export_path"`
}

type searchResult struct {
	Repository string       `json:"repository"`
	Downloads  int64        `json:"downloads"`
	Likes      int64        `json:"likes"`
	Pipeline   string       `json:"pipeline,omitempty"`
	Plan       planner.Plan `json:"plan"`
}

func Run(ctx context.Context, client *hub.Client, options Options) error {
	if options.Writer == nil {
		options.Writer = os.Stdout
	}
	root, err := install.DefaultRoot()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	token, err := randomID(32)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	server := &Server{client: client, token: token, root: root, ctx: runCtx, plans: map[string]storedPlan{}, jobs: map[string]*Job{}, cancel: cancel}
	server.origin = "http://" + listener.Addr().String()
	httpServer := &http.Server{
		Handler: server.routes(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 60 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
			return
		}
		serveErrors <- nil
	}()
	fmt.Fprintf(options.Writer, "SaLMon Model Companion UI: %s\n", server.origin)
	if options.OpenBrowser {
		if err := openBrowser(server.origin); err != nil {
			fmt.Fprintf(options.Writer, "Open this URL in your browser: %s\n", server.origin)
		}
	}
	select {
	case <-runCtx.Done():
	case err := <-serveErrors:
		cancel()
		return err
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	return httpServer.Shutdown(shutdownCtx)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("GET /api/recommendations", s.recommendations)
	mux.HandleFunc("GET /api/providers/{id}/models", s.providerModels)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/inspect", s.inspect)
	mux.HandleFunc("POST /api/install-plan", s.installPlan)
	mux.HandleFunc("POST /api/install", s.startInstall)
	mux.HandleFunc("GET /api/jobs/{id}", s.job)
	mux.HandleFunc("DELETE /api/jobs/{id}", s.cancelJob)
	mux.HandleFunc("GET /api/installed", s.installed)
	mux.HandleFunc("DELETE /api/installed/{id}", s.remove)
	mux.HandleFunc("GET /api/project", s.projectManifest)
	mux.HandleFunc("POST /api/project/assign", s.assignProject)
	mux.HandleFunc("DELETE /api/project/assignment", s.removeProjectAssignment)
	mux.HandleFunc("POST /api/shutdown", s.shutdown)
	return s.securityHeaders(mux)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if r.Method != http.MethodGet {
			if r.Header.Get("Origin") != s.origin || r.Header.Get("X-Salmon-Token") != s.token {
				writeError(w, http.StatusForbidden, errors.New("invalid local UI authorization"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := assets.ReadFile("static/index.html")
	if err != nil {
		writeError(w, 500, err)
		return
	}
	data = []byte(strings.ReplaceAll(string(data), "__SALMON_TOKEN__", s.token))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) recommendations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"providers": recommend.Providers(), "models": recommend.Models(),
		"policy":  "Provider identities and the small starter set are reviewed by SaLMon. Provider inventories are fetched live from Hugging Face and are not stored.",
		"warning": "A recommended source is not certification of every model, compatibility, output quality, safety, or commercial licensing.",
	})
}

func (s *Server) providerModels(w http.ResponseWriter, r *http.Request) {
	provider, found := recommend.ProviderByID(r.PathValue("id"))
	if !found {
		writeError(w, 404, errors.New("recommended provider was not found"))
		return
	}
	limit, err := strconv.Atoi(defaultValue(r.URL.Query().Get("limit"), "20"))
	if err != nil {
		writeError(w, 400, errors.New("invalid result limit"))
		return
	}
	models, err := s.client.Search(r.Context(), hub.SearchOptions{
		Query: r.URL.Query().Get("q"), Format: "gguf", Limit: limit, Author: provider.Account,
	})
	if err != nil {
		writeError(w, 502, err)
		return
	}
	results := make([]searchResult, 0, len(models))
	for _, model := range models {
		results = append(results, searchResult{Repository: model.ID, Downloads: model.Downloads, Likes: model.Likes, Pipeline: model.PipelineTag, Plan: planner.Build(model)})
	}
	writeJSON(w, 200, map[string]any{
		"provider": provider, "results": results,
		"persistence": "Provider models were fetched live from Hugging Face and were not stored.",
	})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(defaultValue(r.URL.Query().Get("limit"), "20"))
	if err != nil {
		writeError(w, 400, errors.New("invalid result limit"))
		return
	}
	models, err := s.client.Search(r.Context(), hub.SearchOptions{Query: r.URL.Query().Get("q"), Format: defaultValue(r.URL.Query().Get("format"), "gguf"), Limit: limit})
	if err != nil {
		writeError(w, 502, err)
		return
	}
	results := make([]searchResult, 0, len(models))
	for _, model := range models {
		results = append(results, searchResult{Repository: model.ID, Downloads: model.Downloads, Likes: model.Likes, Pipeline: model.PipelineTag, Plan: planner.Build(model)})
	}
	writeJSON(w, 200, map[string]any{"results": results, "persistence": "Live results are not stored."})
}

func (s *Server) inspect(w http.ResponseWriter, r *http.Request) {
	model, err := s.client.Inspect(r.Context(), r.URL.Query().Get("repository"), defaultValue(r.URL.Query().Get("revision"), "main"))
	if err != nil {
		writeError(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"repository": model.ID, "resolved_sha": model.SHA, "downloads": model.Downloads,
		"likes": model.Likes, "pipeline": model.PipelineTag, "plan": planner.Build(model),
		"license_url": model.CardData.LicenseLink, "base_model": model.CardData.BaseModel,
		"source_url": "https://huggingface.co/" + model.ID + "/tree/" + model.SHA,
	})
}

func (s *Server) installPlan(w http.ResponseWriter, r *http.Request) {
	var request installRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	model, err := s.client.Inspect(r.Context(), request.Repository, defaultValue(request.Revision, "main"))
	if err != nil {
		writeError(w, 502, err)
		return
	}
	plan, err := install.BuildPlan(model, request.Filename, s.root)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	now := time.Now()
	s.mu.Lock()
	for digest, candidate := range s.plans {
		if now.After(candidate.ExpiresAt) {
			delete(s.plans, digest)
		}
	}
	s.plans[plan.ConsentDigest] = storedPlan{Plan: plan, ExpiresAt: now.Add(15 * time.Minute)}
	s.mu.Unlock()
	writeJSON(w, 200, plan)
}

func (s *Server) startInstall(w http.ResponseWriter, r *http.Request) {
	var request installRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	s.mu.Lock()
	stored, found := s.plans[request.ConsentDigest]
	if found {
		delete(s.plans, request.ConsentDigest)
	}
	s.mu.Unlock()
	if !found || time.Now().After(stored.ExpiresAt) {
		writeError(w, 400, errors.New("installation plan is missing or expired; review it again"))
		return
	}
	plan := stored.Plan
	if plan.Repository != request.Repository || plan.Filename != request.Filename {
		writeError(w, 400, errors.New("installation request differs from reviewed plan"))
		return
	}
	jobID, err := randomID(16)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	jobCtx, cancel := context.WithCancel(s.ctx)
	job := &Job{ID: jobID, Status: "running", TotalBytes: plan.SizeBytes, cancel: cancel}
	s.mu.Lock()
	s.jobs[jobID] = job
	s.mu.Unlock()
	go s.executeInstall(jobCtx, jobID, plan)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) executeInstall(ctx context.Context, id string, plan install.Plan) {
	record, err := install.Execute(ctx, s.client, plan, plan.ConsentDigest, s.root, install.DefaultMaximumBytes, func(completed, total int64) {
		s.mu.Lock()
		if job := s.jobs[id]; job != nil {
			job.CompletedBytes, job.TotalBytes = completed, total
		}
		s.mu.Unlock()
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	if job == nil {
		return
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			job.Status = "cancelled"
		} else {
			job.Status = "failed"
		}
		job.Error = err.Error()
		return
	}
	job.Status, job.Record = "completed", &record
}

func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	job := cloneJob(s.jobs[r.PathValue("id")])
	s.mu.Unlock()
	if job == nil {
		writeError(w, 404, errors.New("installation job not found"))
		return
	}
	writeJSON(w, 200, job)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	job := s.jobs[r.PathValue("id")]
	if job != nil && job.Status == "running" {
		job.cancel()
	}
	snapshot := cloneJob(job)
	s.mu.Unlock()
	if snapshot == nil {
		writeError(w, 404, errors.New("installation job not found"))
		return
	}
	writeJSON(w, 200, snapshot)
}

func (s *Server) installed(w http.ResponseWriter, r *http.Request) {
	records, err := install.List(s.root)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"models": records, "root": s.root})
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	record, err := install.Remove(s.root, r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"removed": record})
}

func (s *Server) projectManifest(w http.ResponseWriter, r *http.Request) {
	manifest, err := project.Load(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, manifest)
}

func (s *Server) assignProject(w http.ResponseWriter, r *http.Request) {
	var request projectRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	records, err := install.List(s.root)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	var selected *install.Record
	for index := range records {
		if records[index].ID == request.InstallationID {
			selected = &records[index]
			break
		}
	}
	if selected == nil {
		writeError(w, 404, errors.New("installed model was not found"))
		return
	}
	manifest, err := project.Assign(request.ManifestPath, *selected, request.Purposes, request.ExportPath)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, manifest)
}

func (s *Server) removeProjectAssignment(w http.ResponseWriter, r *http.Request) {
	var request projectRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	manifest, err := project.Remove(request.ManifestPath, request.ExportPath)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, manifest)
}

func (s *Server) shutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "shutting_down"})
	go s.cancel()
}

func cloneJob(job *Job) *Job {
	if job == nil {
		return nil
	}
	copy := *job
	copy.cancel = nil
	return &copy
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}
func defaultValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func randomID(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func openBrowser(address string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
		return errors.New("refusing to open unexpected UI address")
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	case "darwin":
		command = exec.Command("open", address)
	default:
		command = exec.Command("xdg-open", address)
	}
	return command.Start()
}
