package webui

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/conversionenv"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/convert"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/install"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/quantize"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/toolchain"
)

type preparationRequest struct {
	Repository     string `json:"repository"`
	Revision       string `json:"revision"`
	InstallationID string `json:"installation_id"`
	Preset         string `json:"preset"`
	Outtype        string `json:"outtype"`
	ConsentDigest  string `json:"consent_digest"`
}
type storedPreparation struct {
	Kind      string
	Plan      any
	ExpiresAt time.Time
}

func (s *Server) preparationToolchains(w http.ResponseWriter, r *http.Request) {
	converter, err := conversionenv.List(s.root)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	quantizer, err := toolchain.List(s.root)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"conversion": converter, "quantization": quantizer})
}
func (s *Server) preparationPlan(w http.ResponseWriter, r *http.Request) {
	var request preparationRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	kind := r.PathValue("kind")
	var plan any
	var digest string
	var err error
	switch kind {
	case "conversion-toolchain":
		var result conversionenv.Plan
		result, err = conversionenv.BuildPlan(s.root)
		plan, digest = result, result.ConsentDigest
	case "quantizer-toolchain":
		var result toolchain.Plan
		result, err = toolchain.BuildPlan(s.root)
		plan, digest = result, result.ConsentDigest
	case "convert":
		if request.Repository == "" || request.Outtype == "" {
			writeError(w, 400, errors.New("repository and output type are required"))
			return
		}
		var modelPlan convert.Plan
		model, inspectErr := s.client.Inspect(r.Context(), request.Repository, defaultValue(request.Revision, "main"))
		if inspectErr != nil {
			err = inspectErr
		} else {
			modelPlan, err = convert.BuildPlan(r.Context(), model, request.Outtype, "", s.root, convert.Dependencies{})
		}
		plan, digest = modelPlan, modelPlan.ConsentDigest
	case "quantize":
		if request.InstallationID == "" || request.Preset == "" {
			writeError(w, 400, errors.New("installed model and preset are required"))
			return
		}
		records, listErr := install.List(s.root)
		if listErr != nil {
			err = listErr
			break
		}
		var selected *install.Record
		for index := range records {
			if records[index].ID == request.InstallationID {
				selected = &records[index]
				break
			}
		}
		if selected == nil {
			err = errors.New("installed model was not found")
			break
		}
		var result quantize.Plan
		result, err = quantize.BuildPlan(r.Context(), selected.Path, request.Preset, "", s.root, quantize.Dependencies{})
		plan, digest = result, result.ConsentDigest
	default:
		writeError(w, 404, errors.New("unknown preparation action"))
		return
	}
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if digest == "" {
		writeError(w, 500, errors.New("preparation plan has no consent digest"))
		return
	}
	s.mu.Lock()
	if s.preparationPlans == nil {
		s.preparationPlans = map[string]storedPreparation{}
	}
	now := time.Now()
	for key, value := range s.preparationPlans {
		if now.After(value.ExpiresAt) {
			delete(s.preparationPlans, key)
		}
	}
	s.preparationPlans[digest] = storedPreparation{kind, plan, now.Add(15 * time.Minute)}
	s.mu.Unlock()
	writeJSON(w, 200, plan)
}
func (s *Server) startPreparation(w http.ResponseWriter, r *http.Request) {
	var request preparationRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	kind := r.PathValue("kind")
	s.mu.Lock()
	stored, found := s.preparationPlans[request.ConsentDigest]
	if !found || stored.Kind != kind || time.Now().After(stored.ExpiresAt) {
		s.mu.Unlock()
		writeError(w, 400, errors.New("preparation plan is missing or expired; review it again"))
		return
	}
	// Toolchain installers use shared staging paths; do not launch competing preparation jobs.
	for _, job := range s.jobs {
		if job.Status == "running" {
			s.mu.Unlock()
			writeError(w, 409, errors.New("another model operation is running; review a new plan after it finishes"))
			return
		}
	}
	delete(s.preparationPlans, request.ConsentDigest)
	id, err := randomID(16)
	if err != nil {
		s.mu.Unlock()
		writeError(w, 500, err)
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	job := &Job{ID: id, Status: "running", Stage: "starting", cancel: cancel}
	s.jobs[id] = job
	s.mu.Unlock()
	go s.executePreparation(ctx, id, stored)
	writeJSON(w, http.StatusAccepted, cloneJob(job))
}
func (s *Server) executePreparation(ctx context.Context, id string, stored storedPreparation) {
	progress := func(stage, message string, completed, total int64) {
		s.mu.Lock()
		if job := s.jobs[id]; job != nil {
			job.Stage, job.Message, job.CompletedBytes, job.TotalBytes = stage, message, completed, total
		}
		s.mu.Unlock()
	}
	var record *install.Record
	var result any
	var err error
	switch plan := stored.Plan.(type) {
	case conversionenv.Plan:
		var item conversionenv.Record
		item, err = conversionenv.Execute(ctx, plan, plan.ConsentDigest, s.root, conversionenv.DefaultMaximumComponentBytes, progress, conversionenv.Dependencies{})
		result = item
	case toolchain.Plan:
		var item toolchain.Record
		item, err = toolchain.Execute(ctx, plan, plan.ConsentDigest, s.root, toolchain.DefaultMaximumArchive, func(completed, total int64) {
			progress("download", "Downloading pinned quantizer archive", completed, total)
		}, toolchain.Dependencies{})
		result = item
	case convert.Plan:
		var item install.Record
		item, err = convert.Execute(ctx, s.client, plan, plan.ConsentDigest, s.root, convert.DefaultMaximumSourceBytes, progress, convert.Dependencies{})
		record = &item
	case quantize.Plan:
		var item install.Record
		item, err = quantize.Execute(ctx, plan, plan.ConsentDigest, s.root, quantize.DefaultMaximumOutputBytes, func(message string) { progress("quantize", message, 0, 0) }, quantize.Dependencies{})
		record = &item
	default:
		err = errors.New("unexpected preparation plan")
	}
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
	job.Status = "completed"
	job.Record = record
	if result != nil {
		job.Toolchain = result
	}
	job.Message = "Operation completed and verified"
}
