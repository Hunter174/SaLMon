package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const DefaultEndpoint = "https://huggingface.co"
const maxMetadataBytes = 16 << 20

var componentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Client struct {
	endpoint string
	http     *http.Client
}

type SearchOptions struct {
	Query  string
	Format string
	Limit  int
}

type ProgressFunc func(completed, total int64)

type Model struct {
	ID           string          `json:"id"`
	SHA          string          `json:"sha"`
	PipelineTag  string          `json:"pipeline_tag,omitempty"`
	LibraryName  string          `json:"library_name,omitempty"`
	Tags         []string        `json:"tags,omitempty"`
	Downloads    int64           `json:"downloads,omitempty"`
	Likes        int64           `json:"likes,omitempty"`
	Gated        json.RawMessage `json:"gated,omitempty"`
	Private      bool            `json:"private,omitempty"`
	Disabled     bool            `json:"disabled,omitempty"`
	LastModified string          `json:"lastModified,omitempty"`
	Siblings     []File          `json:"siblings,omitempty"`
	Config       ModelConfig     `json:"config,omitempty"`
	CardData     CardData        `json:"cardData,omitempty"`
	GGUF         *GGUFMetadata   `json:"gguf,omitempty"`
}

type ModelConfig struct {
	ModelType     string   `json:"model_type,omitempty"`
	Architectures []string `json:"architectures,omitempty"`
}

type CardData struct {
	License     string `json:"license,omitempty"`
	LicenseLink string `json:"license_link,omitempty"`
	BaseModel   any    `json:"base_model,omitempty"`
}

type GGUFMetadata struct {
	Total         int64  `json:"total,omitempty"`
	Architecture  string `json:"architecture,omitempty"`
	ContextLength int64  `json:"context_length,omitempty"`
	TotalFileSize int64  `json:"totalFileSize,omitempty"`
	ChatTemplate  string `json:"chat_template,omitempty"`
}

type File struct {
	Name   string   `json:"rfilename"`
	Size   int64    `json:"size,omitempty"`
	BlobID string   `json:"blobId,omitempty"`
	LFS    *LFSInfo `json:"lfs,omitempty"`
}

type LFSInfo struct {
	SHA256 string `json:"sha256,omitempty"`
	OID    string `json:"oid,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

func (f File) ContentSHA256() string {
	if f.LFS == nil {
		return ""
	}
	value := f.LFS.SHA256
	if value == "" {
		value = f.LFS.OID
	}
	return strings.TrimPrefix(strings.ToLower(value), "sha256:")
}

func New(endpoint string) (*Client, error) {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid Hugging Face endpoint")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" {
		return nil, errors.New("Hugging Face endpoint must use HTTPS")
	}
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), http: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (c *Client) Search(ctx context.Context, options SearchOptions) ([]Model, error) {
	if len(options.Query) > 200 {
		return nil, errors.New("search query exceeds 200 characters")
	}
	if options.Limit < 1 || options.Limit > 100 {
		return nil, errors.New("search limit must be between 1 and 100")
	}
	if options.Format != "any" && options.Format != "gguf" {
		return nil, errors.New("format must be any or gguf")
	}
	values := url.Values{
		"search": {options.Query}, "sort": {"downloads"}, "direction": {"-1"},
		"limit": {strconv.Itoa(options.Limit)}, "full": {"true"}, "config": {"true"},
	}
	if options.Format == "gguf" {
		values.Set("filter", "gguf")
	}
	var models []Model
	if err := c.getJSON(ctx, c.endpoint+"/api/models?"+values.Encode(), &models); err != nil {
		return nil, err
	}
	return models, nil
}

func (c *Client) Inspect(ctx context.Context, repository, revision string) (Model, error) {
	owner, name, err := repositoryParts(repository)
	if err != nil {
		return Model{}, err
	}
	if revision == "" {
		revision = "main"
	}
	if len(revision) > 200 || strings.ContainsAny(revision, "?#") {
		return Model{}, errors.New("invalid revision")
	}
	path := "/api/models/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/revision/" + url.PathEscape(revision)
	var model Model
	if err := c.getJSON(ctx, c.endpoint+path+"?blobs=true", &model); err != nil {
		return Model{}, err
	}
	if model.ID == "" || model.SHA == "" {
		return Model{}, errors.New("Hugging Face returned incomplete model identity")
	}
	return model, nil
}

func repositoryParts(repository string) (string, string, error) {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !componentPattern.MatchString(parts[0]) || !componentPattern.MatchString(parts[1]) {
		return "", "", errors.New("repository must be owner/name using safe Hugging Face characters")
	}
	return parts[0], parts[1], nil
}

func (c *Client) DownloadFile(ctx context.Context, repository, revision, filename string, expectedSize, maximumSize int64, destination io.Writer, progress ProgressFunc) error {
	owner, name, err := repositoryParts(repository)
	if err != nil {
		return err
	}
	if revision == "" || len(revision) > 200 || strings.ContainsAny(revision, "?#") {
		return errors.New("invalid resolved revision")
	}
	if filename == "" || len(filename) > 1000 || strings.Contains(filename, "\\") {
		return errors.New("invalid repository filename")
	}
	segments := strings.Split(filename, "/")
	for index, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("invalid repository filename")
		}
		segments[index] = url.PathEscape(segment)
	}
	address := c.endpoint + "/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/resolve/" + url.PathEscape(revision) + "/" + strings.Join(segments, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "salmon-model/0.1 (+https://github.com/Hunter174/SaLMon)")
	client := *c.http
	// Metadata requests are short-lived, but multi-gigabyte model downloads must
	// be bounded by their context, size limit, and caller cancellation instead.
	client.Timeout = 0
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" && request.URL.Hostname() != "127.0.0.1" && request.URL.Hostname() != "localhost" {
			return errors.New("download redirect must use HTTPS")
		}
		if len(via) >= 10 {
			return errors.New("too many download redirects")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Hugging Face download failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Hugging Face download returned HTTP %d", response.StatusCode)
	}
	if expectedSize <= 0 || maximumSize <= 0 || expectedSize > maximumSize {
		return errors.New("download size is missing or exceeds the configured limit")
	}
	if response.ContentLength > 0 && response.ContentLength != expectedSize {
		return fmt.Errorf("download Content-Length %d does not match planned size %d", response.ContentLength, expectedSize)
	}
	reader := io.LimitReader(response.Body, maximumSize+1)
	buffer := make([]byte, 256*1024)
	var completed int64
	for {
		count, readErr := reader.Read(buffer)
		if count > 0 {
			completed += int64(count)
			if completed > maximumSize {
				return errors.New("download exceeded configured size limit")
			}
			if _, err := destination.Write(buffer[:count]); err != nil {
				return fmt.Errorf("write download: %w", err)
			}
			if progress != nil {
				progress(completed, expectedSize)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read download: %w", readErr)
		}
	}
	if completed != expectedSize {
		return fmt.Errorf("downloaded size %d does not match planned size %d", completed, expectedSize)
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, address string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "salmon-model/0.1 (+https://github.com/Hunter174/SaLMon)")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("Hugging Face request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Hugging Face request returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxMetadataBytes))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid Hugging Face response: %w", err)
	}
	return nil
}
