package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/install"
)

const Filename = "salmon.models.json"

var exportPathPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*\.gguf$`)

type Manifest struct {
	SchemaVersion int          `json:"schema_version"`
	Models        []Assignment `json:"models"`
}

type Assignment struct {
	Purposes       []string `json:"purposes"`
	InstallationID string   `json:"installation_id"`
	SHA256         string   `json:"sha256"`
	Repository     string   `json:"repository"`
	ResolvedSHA    string   `json:"resolved_sha"`
	Filename       string   `json:"filename"`
	ExportPath     string   `json:"export_path"`
	Required       bool     `json:"required"`
	License        string   `json:"license"`
	LicenseURL     string   `json:"license_url,omitempty"`
	BaseModel      any      `json:"base_model,omitempty"`
	SourceURL      string   `json:"source_url"`
}

func Load(filename string) (Manifest, error) {
	filename, err := validateFilename(filename)
	if err != nil {
		return Manifest{}, err
	}
	data, err := os.ReadFile(filename)
	if os.IsNotExist(err) {
		return Manifest{SchemaVersion: 1, Models: []Assignment{}}, nil
	}
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("invalid project manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 {
		return Manifest{}, fmt.Errorf("unsupported project manifest schema %d", manifest.SchemaVersion)
	}
	if manifest.Models == nil {
		manifest.Models = []Assignment{}
	}
	return manifest, nil
}

func Assign(filename string, record install.Record, purposes []string, exportPath string) (Manifest, error) {
	filename, err := validateFilename(filename)
	if err != nil {
		return Manifest{}, err
	}
	purposes, err = validatePurposes(purposes)
	if err != nil {
		return Manifest{}, err
	}
	exportPath = strings.ReplaceAll(exportPath, "\\", "/")
	if !exportPathPattern.MatchString(exportPath) || strings.HasPrefix(exportPath, "/") || pathpkg.Clean(exportPath) != exportPath {
		return Manifest{}, errors.New("export path must be a safe relative .gguf path")
	}
	manifest, err := Load(filename)
	if err != nil {
		return Manifest{}, err
	}
	assignment := Assignment{
		Purposes: purposes, InstallationID: record.ID, SHA256: record.SHA256,
		Repository: record.Repository, ResolvedSHA: record.ResolvedSHA, Filename: record.Filename,
		ExportPath: exportPath, Required: true, License: record.License,
		LicenseURL: record.LicenseURL, BaseModel: record.BaseModel, SourceURL: record.SourceURL,
	}
	replaced := false
	for index := range manifest.Models {
		if manifest.Models[index].ExportPath == exportPath {
			manifest.Models[index], replaced = assignment, true
			break
		}
	}
	if !replaced {
		manifest.Models = append(manifest.Models, assignment)
	}
	sort.Slice(manifest.Models, func(i, j int) bool { return manifest.Models[i].ExportPath < manifest.Models[j].ExportPath })
	if err := writeAtomic(filename, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func Remove(filename, exportPath string) (Manifest, error) {
	filename, err := validateFilename(filename)
	if err != nil {
		return Manifest{}, err
	}
	exportPath = strings.ReplaceAll(exportPath, "\\", "/")
	manifest, err := Load(filename)
	if err != nil {
		return Manifest{}, err
	}
	models := make([]Assignment, 0, len(manifest.Models))
	found := false
	for _, assignment := range manifest.Models {
		if assignment.ExportPath == exportPath {
			found = true
			continue
		}
		models = append(models, assignment)
	}
	if !found {
		return Manifest{}, errors.New("project assignment was not found")
	}
	manifest.Models = models
	if err := writeAtomic(filename, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateFilename(filename string) (string, error) {
	if strings.TrimSpace(filename) == "" {
		return "", errors.New("project manifest path is required")
	}
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return "", err
	}
	if filepath.Base(absolute) != Filename {
		return "", fmt.Errorf("project manifest must be named %s", Filename)
	}
	return absolute, nil
}

func validatePurposes(values []string) ([]string, error) {
	allowed := map[string]bool{"chat": true, "decision": true, "embedding": true}
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if !allowed[value] {
			return nil, fmt.Errorf("unsupported model purpose %q", value)
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("at least one model purpose is required")
	}
	sort.Strings(result)
	return result, nil
}

func writeAtomic(filename string, manifest Manifest) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary := filename + ".tmp"
	backup := filename + ".bak"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return err
	}
	_ = os.Remove(backup)
	hadPrevious := false
	if _, err := os.Stat(filename); err == nil {
		if err := os.Rename(filename, backup); err != nil {
			_ = os.Remove(temporary)
			return err
		}
		hadPrevious = true
	} else if !os.IsNotExist(err) {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, filename); err != nil {
		if hadPrevious {
			_ = os.Rename(backup, filename)
		}
		_ = os.Remove(temporary)
		return err
	}
	_ = os.Remove(backup)
	return nil
}
