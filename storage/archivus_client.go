package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"

	fetcherconfig "dolores/fetcher/config"
)

const defaultBaseURL = "http://localhost:8080"

// FileInfo describes a single entry returned by the list endpoint.
type FileInfo struct {
	ID             string  `json:"ID"`
	Name           string  `json:"Name"`
	IsDir          bool    `json:"IsDir"`
	Extension      string  `json:"Extension"`
	SignedURL      string  `json:"SignedUrl"`
	Size           float64 `json:"Size"`
	Path           string  `json:"Path"`
	Thumbnail      string  `json:"Thumbnail"`
	NavigationPath string  `json:"NavigationPath"`
}

// UploadFile is a file to be uploaded.
type UploadFile struct {
	Name    string
	Content []byte
}

// ArchivusClient is a minimal client for the Archivus file management API.
type ArchivusClient struct {
	baseURL string
	apiKey  string
	folder  string

	hc      *http.Client
	mu      sync.Mutex
	driveID string
}

// NewArchivusClient creates a client scoped to parentFolder. The base URL is
// read from ARCHIVUS_BASE_URL in the environment (via fetcher/config).
func NewArchivusClient(apiKey, parentFolder string) *ArchivusClient {
	baseURL := strings.TrimRight(fetcherconfig.ARCHIVUS_BASE_URL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	return &ArchivusClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		folder:  parentFolder,
		hc:      &http.Client{Timeout: 30 * time.Second},
	}
}

type userInfo struct {
	Drives []struct {
		DriveID string `json:"DriveID"`
	} `json:"drives"`
}

type listResponse struct {
	Files []FileInfo `json:"files"`
}

// EnsureFolder creates folderPath (relative to the client's parent folder) if
// it does not already exist. It creates intermediate directories as needed.
func (c *ArchivusClient) EnsureFolder(folderPath string) error {
	full := c.resolvePath(folderPath)
	if full == "" {
		return nil
	}

	exists, err := c.folderExists(full)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return c.createFolder(full)
}

// Upload stores files in folderPath (relative to the client's parent folder).
func (c *ArchivusClient) Upload(folderPath string, files []*UploadFile) error {
	if len(files) == 0 {
		return nil
	}

	if err := c.EnsureFolder(folderPath); err != nil {
		return err
	}
	full := c.resolvePath(folderPath)

	driveID, err := c.getDriveID()
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	if err := w.WriteField("folderPath", full); err != nil {
		return err
	}
	if err := w.WriteField("driveId", driveID); err != nil {
		return err
	}

	for _, f := range files {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="files"; filename="%s"`, escapeQuotes(f.Name)))
		h.Set("Content-Type", "application/octet-stream")

		part, err := w.CreatePart(h)
		if err != nil {
			return err
		}
		if _, err := part.Write(f.Content); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/storage/file/upload", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	return c.do(req, nil)
}

// List returns the entries in folderPath (relative to the client's parent
// folder). An empty string lists the drive root.
func (c *ArchivusClient) List(folderPath string) ([]FileInfo, error) {
	return c.list(c.resolvePath(folderPath))
}

func (c *ArchivusClient) list(full string) ([]FileInfo, error) {
	driveID, err := c.getDriveID()
	if err != nil {
		return nil, err
	}

	req, err := c.newJSONRequest(http.MethodPost, "/storage/files", map[string]string{
		"path":    full,
		"driveId": driveID,
	})
	if err != nil {
		return nil, err
	}

	var out listResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return out.Files, nil
}

// Download fetches the raw bytes of a file by ID and returns them together
// with the server-provided filename.
func (c *ArchivusClient) Download(fileID string) ([]byte, string, error) {
	driveID, err := c.getDriveID()
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/storage/file/download", nil)
	if err != nil {
		return nil, "", err
	}
	q := req.URL.Query()
	q.Set("fileId", fileID)
	q.Set("driveId", driveID)
	req.URL.RawQuery = q.Encode()

	c.setAuth(req)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("archivus: %s: %s", resp.Status, string(body))
	}

	return body, parseFilename(resp.Header.Get("Content-Disposition")), nil
}

func (c *ArchivusClient) createFolder(full string) error {
	driveID, err := c.getDriveID()
	if err != nil {
		return err
	}

	req, err := c.newJSONRequest(http.MethodPost, "/storage/folder/create", map[string]string{
		"path":    full,
		"driveId": driveID,
	})
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *ArchivusClient) folderExists(full string) (bool, error) {
	parent, name := splitPath(full)
	entries, err := c.list(parent)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.IsDir && e.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// getDriveID resolves and caches the drive ID from the user info endpoint.
func (c *ArchivusClient) getDriveID() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.driveID != "" {
		return c.driveID, nil
	}

	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/auth/user/info", nil)
	if err != nil {
		return "", err
	}
	c.setAuth(req)

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("archivus: %s: %s", resp.Status, string(body))
	}

	var info userInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return "", err
	}
	if len(info.Drives) == 0 || info.Drives[0].DriveID == "" {
		return "", fmt.Errorf("archivus: no drive found for API key")
	}

	c.driveID = info.Drives[0].DriveID
	return c.driveID, nil
}

func (c *ArchivusClient) newJSONRequest(method, path string, payload any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)
	return req, nil
}

func (c *ArchivusClient) setAuth(req *http.Request) {
	req.Header.Set("X-API-Key", c.apiKey)
}

func (c *ArchivusClient) do(req *http.Request, out any) error {
	c.setAuth(req)

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("archivus: %s: %s", resp.Status, string(body))
	}
	if out != nil && len(body) > 0 {
		return json.Unmarshal(body, out)
	}
	return nil
}

// resolvePath prepends the client's parent folder, keeping the result relative
// to the drive root.
func (c *ArchivusClient) resolvePath(p string) string {
	return joinPath(c.folder, p)
}

func joinPath(folder, sub string) string {
	folder = strings.Trim(folder, "/")
	sub = strings.Trim(sub, "/")
	switch {
	case folder == "":
		return sub
	case sub == "":
		return folder
	default:
		return folder + "/" + sub
	}
}

func splitPath(p string) (parent, name string) {
	p = strings.Trim(p, "/")
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return "", p
	}
	return p[:idx], p[idx+1:]
}

func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

func parseFilename(cd string) string {
	for _, part := range strings.Split(cd, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "filename=") {
			return strings.Trim(strings.TrimPrefix(part, "filename="), `"`)
		}
	}
	return ""
}
