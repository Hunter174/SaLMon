package install

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/planner"
)

const DefaultMaximumBytes int64 = 20 << 30

var installationIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type Plan struct {
	SchemaVersion int    `json:"schema_version"`
	Repository    string `json:"repository"`
	ResolvedSHA   string `json:"resolved_sha"`
	Filename      string `json:"filename"`
	SizeBytes     int64  `json:"size_bytes"`
	SHA256        string `json:"sha256"`
	License       string `json:"license"`
	LicenseURL    string `json:"license_url,omitempty"`
	SourceURL     string `json:"source_url"`
	Destination   string `json:"destination"`
	ConsentDigest string `json:"consent_digest,omitempty"`
	Warning       string `json:"warning"`
}

type Record struct {
	SchemaVersion        int    `json:"schema_version"`
	ID                   string `json:"id"`
	Repository           string `json:"repository"`
	ResolvedSHA          string `json:"resolved_sha"`
	Filename             string `json:"filename"`
	SizeBytes            int64  `json:"size_bytes"`
	SHA256               string `json:"sha256"`
	License              string `json:"license"`
	LicenseURL           string `json:"license_url,omitempty"`
	SourceURL            string `json:"source_url"`
	Path                 string `json:"path"`
	InstalledAt          string `json:"installed_at"`
	StructuralValidation string `json:"structural_validation"`
	RuntimeValidation    string `json:"runtime_validation"`
}

type ProgressFunc func(completed, total int64)

func DefaultRoot() (string, error) {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return "", errors.New("LOCALAPPDATA is not set")
		}
		return filepath.Join(base, "SaLMon"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "SaLMon"), nil
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "salmon"), nil
}

func BuildPlan(model hub.Model, filename, root string) (Plan, error) {
	if root == "" {
		var err error
		root, err = DefaultRoot()
		if err != nil {
			return Plan{}, err
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Plan{}, err
	}
	modelPlan := planner.Build(model)
	var selected *planner.GGUFFile
	for index := range modelPlan.GGUFFiles {
		if modelPlan.GGUFFiles[index].Name == filename {
			selected = &modelPlan.GGUFFiles[index]
			break
		}
	}
	if selected == nil {
		return Plan{}, errors.New("selected file is not a GGUF in the resolved repository")
	}
	if selected.SizeBytes <= 0 {
		return Plan{}, errors.New("Hugging Face did not report the selected file size")
	}
	if len(selected.SHA256) != 64 {
		return Plan{}, errors.New("Hugging Face did not report a usable SHA-256 for the selected file")
	}
	localName := safeBaseName(filename)
	destination := filepath.Join(root, "models", "sha256", selected.SHA256, localName)
	plan := Plan{
		SchemaVersion: 1, Repository: model.ID, ResolvedSHA: model.SHA,
		Filename: filename, SizeBytes: selected.SizeBytes, SHA256: selected.SHA256,
		License: modelPlan.License, LicenseURL: model.CardData.LicenseLink,
		SourceURL: "https://huggingface.co/" + model.ID + "/tree/" + model.SHA, Destination: destination,
		Warning: "Installation is local-only. Live Hugging Face metadata is not a SaLMon compatibility certification.",
	}
	plan.ConsentDigest, err = Digest(plan)
	return plan, err
}

func Digest(plan Plan) (string, error) {
	plan.ConsentDigest = ""
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func Execute(ctx context.Context, client *hub.Client, plan Plan, consent, root string, maximumBytes int64, progress ProgressFunc) (Record, error) {
	digest, err := Digest(plan)
	if err != nil {
		return Record{}, err
	}
	if consent == "" || !strings.EqualFold(consent, digest) || !strings.EqualFold(plan.ConsentDigest, digest) {
		return Record{}, errors.New("consent digest does not match the exact installation plan")
	}
	if maximumBytes <= 0 {
		maximumBytes = DefaultMaximumBytes
	}
	if plan.SizeBytes > maximumBytes {
		return Record{}, errors.New("planned file exceeds the configured maximum size")
	}
	if root == "" {
		root, err = DefaultRoot()
		if err != nil {
			return Record{}, err
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Record{}, err
	}
	expectedDestination := filepath.Join(root, "models", "sha256", plan.SHA256, safeBaseName(plan.Filename))
	if filepath.Clean(plan.Destination) != filepath.Clean(expectedDestination) {
		return Record{}, errors.New("installation destination differs from the consented plan")
	}
	if err := os.MkdirAll(filepath.Dir(expectedDestination), 0o755); err != nil {
		return Record{}, err
	}
	downloadDirectory := filepath.Join(root, "downloads")
	if err := os.MkdirAll(downloadDirectory, 0o755); err != nil {
		return Record{}, err
	}
	temporary := filepath.Join(downloadDirectory, digest+".part")
	_ = os.Remove(temporary)
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Record{}, err
	}
	removeTemporary := true
	defer func() {
		file.Close()
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()
	hasher := sha256.New()
	writer := io.MultiWriter(file, hasher)
	err = client.DownloadFile(ctx, plan.Repository, plan.ResolvedSHA, plan.Filename, plan.SizeBytes, maximumBytes, writer,
		func(completed, total int64) {
			if progress != nil {
				progress(completed, total)
			}
		})
	if err != nil {
		return Record{}, err
	}
	if err := file.Sync(); err != nil {
		return Record{}, err
	}
	if err := file.Close(); err != nil {
		return Record{}, err
	}
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualHash, plan.SHA256) {
		return Record{}, fmt.Errorf("SHA-256 mismatch: expected %s, got %s", plan.SHA256, actualHash)
	}
	if err := validateGGUF(temporary); err != nil {
		return Record{}, err
	}
	if existing, err := os.Stat(expectedDestination); err == nil {
		if existing.Size() != plan.SizeBytes {
			return Record{}, errors.New("existing content-addressed model has the wrong size")
		}
		existingHash, err := hashFile(expectedDestination)
		if err != nil {
			return Record{}, err
		}
		if !strings.EqualFold(existingHash, plan.SHA256) {
			return Record{}, errors.New("existing content-addressed model is corrupt")
		}
		_ = os.Remove(temporary)
	} else if !os.IsNotExist(err) {
		return Record{}, err
	} else if err := os.Rename(temporary, expectedDestination); err != nil {
		return Record{}, fmt.Errorf("atomically install model: %w", err)
	}
	removeTemporary = false
	record := Record{
		SchemaVersion: 1, ID: installationID(plan), Repository: plan.Repository, ResolvedSHA: plan.ResolvedSHA,
		Filename: plan.Filename, SizeBytes: plan.SizeBytes, SHA256: strings.ToLower(plan.SHA256), License: plan.License,
		LicenseURL: plan.LicenseURL, SourceURL: plan.SourceURL,
		Path: expectedDestination, InstalledAt: time.Now().UTC().Format(time.RFC3339),
		StructuralValidation: "gguf-header-passed", RuntimeValidation: "not-run",
	}
	if err := writeRecord(root, record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func List(root string) ([]Record, error) {
	var err error
	if root == "" {
		root, err = DefaultRoot()
		if err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "registry"))
	if os.IsNotExist(err) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "registry", entry.Name()))
		if err != nil {
			return nil, err
		}
		var record Record
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("invalid installation record %s: %w", entry.Name(), err)
		}
		records = append(records, record)
	}
	return records, nil
}

func Remove(root, id string) (Record, error) {
	if !installationIDPattern.MatchString(id) {
		return Record{}, errors.New("installation ID must be 32 lowercase hexadecimal characters")
	}
	var err error
	if root == "" {
		root, err = DefaultRoot()
		if err != nil {
			return Record{}, err
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Record{}, err
	}
	recordPath := filepath.Join(root, "registry", id+".json")
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, err
	}
	if record.ID != id || len(record.SHA256) != 64 {
		return Record{}, errors.New("installation record identity is invalid")
	}
	expectedPath := filepath.Join(root, "models", "sha256", record.SHA256, safeBaseName(record.Filename))
	if filepath.Clean(record.Path) != filepath.Clean(expectedPath) {
		return Record{}, errors.New("installation record path escapes managed storage")
	}
	deletingPath := recordPath + ".deleting"
	if err := os.Rename(recordPath, deletingPath); err != nil {
		return Record{}, err
	}
	restore := true
	defer func() {
		if restore {
			_ = os.Rename(deletingPath, recordPath)
		}
	}()
	records, err := List(root)
	if err != nil {
		return Record{}, err
	}
	stillReferenced := false
	for _, other := range records {
		if filepath.Clean(other.Path) == filepath.Clean(record.Path) {
			stillReferenced = true
			break
		}
	}
	if !stillReferenced {
		if err := os.Remove(expectedPath); err != nil && !os.IsNotExist(err) {
			return Record{}, err
		}
		_ = os.Remove(filepath.Dir(expectedPath))
	}
	if err := os.Remove(deletingPath); err != nil {
		return Record{}, err
	}
	restore = false
	return record, nil
}

func writeRecord(root string, record Record) error {
	directory := filepath.Join(root, "registry")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary := filepath.Join(directory, record.ID+".json.tmp")
	final := filepath.Join(directory, record.ID+".json")
	if existing, err := os.ReadFile(final); err == nil {
		var previous Record
		if json.Unmarshal(existing, &previous) != nil || previous.SHA256 != record.SHA256 || previous.Path != record.Path {
			return errors.New("installation registry contains a conflicting record")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, final); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func installationID(plan Plan) string {
	digest := sha256.Sum256([]byte(plan.Repository + "\n" + plan.ResolvedSHA + "\n" + plan.Filename))
	return hex.EncodeToString(digest[:16])
}

func safeBaseName(filename string) string {
	name := path.Base(filename)
	var result strings.Builder
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._-", character) {
			result.WriteRune(character)
		} else {
			result.WriteByte('_')
		}
	}
	if result.Len() == 0 {
		return "model.gguf"
	}
	return result.String()
}

func hashFile(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func validateGGUF(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 8)
	if _, err := io.ReadFull(file, header); err != nil {
		return errors.New("download is too small to be GGUF")
	}
	if string(header[:4]) != "GGUF" {
		return errors.New("download does not have GGUF magic")
	}
	version := binary.LittleEndian.Uint32(header[4:])
	if version < 2 || version > 3 {
		return fmt.Errorf("unsupported GGUF version %d", version)
	}
	return nil
}
