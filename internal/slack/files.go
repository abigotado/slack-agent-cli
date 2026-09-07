package slack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/errx"
)

// File is untrusted attachment metadata. URLs and previews are deliberately omitted.
type File struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Title      string `json:"title,omitempty"`
	Mimetype   string `json:"mimetype,omitempty"`
	Filetype   string `json:"filetype,omitempty"`
	Size       int64  `json:"size"`
	Mode       string `json:"mode,omitempty"`
	IsExternal bool   `json:"is_external"`
}

// FileDetails keeps the download locator private to the Slack transport.
type FileDetails struct {
	File
	downloadURL string
}

// DownloadResult describes verified bytes; it never contains file contents.
type DownloadResult struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// FileInfo reads one exact file after the caller verifies its conversation attachment.
func (c *Client) FileInfo(ctx context.Context, token Token, fileID string) (FileDetails, error) {
	if !validSlackID(fileID) || !strings.HasPrefix(fileID, "F") {
		return FileDetails{}, errx.New(errx.Usage, "INVALID_FILE_ID", "file ID is invalid", "use an exact Slack file ID")
	}
	var response struct {
		File struct {
			File
			Private  string `json:"url_private"`
			Download string `json:"url_private_download"`
		} `json:"file"`
	}
	if err := c.call(ctx, token, http.MethodGet, "files.info", url.Values{"file": {fileID}, "limit": {"1"}}, &response, readOperation); err != nil {
		return FileDetails{}, err
	}
	if response.File.ID != fileID || response.File.Size < 0 {
		return FileDetails{}, errx.New(errx.Internal, "FILE_IDENTITY_MISMATCH", "Slack returned invalid file metadata", "re-read the exact attachment")
	}
	locator := response.File.Download
	if locator == "" {
		locator = response.File.Private
	}
	return FileDetails{File: response.File.File, downloadURL: locator}, nil
}

func fileDownloadURL(file FileDetails, workspaceID string) (*url.URL, error) {
	denied := errx.New(errx.PermissionDenied, "FILE_DOWNLOAD_UNSUPPORTED", "file does not have a supported private Slack download", "use a hosted file with the selected workspace owner; Enterprise Grid E-owned files, external files and redirects are unsupported")
	if !validSlackID(workspaceID) || !strings.HasPrefix(workspaceID, "T") || !validSlackID(file.ID) || !strings.HasPrefix(file.ID, "F") || file.Mode != "hosted" || file.IsExternal {
		return nil, denied
	}
	u, err := url.Parse(file.downloadURL)
	if err != nil || u.Scheme+"://"+u.Host != contract.ProductionFileOrigin || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, denied
	}
	prefix := contract.FileDownloadPathPrefix + workspaceID + "-" + file.ID + "/"
	if !strings.HasPrefix(u.Path, prefix) || !strings.HasPrefix(u.EscapedPath(), prefix) || strings.ContainsAny(u.Path, "\\\x00\r\n") {
		return nil, denied
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return nil, denied
		}
	}
	if len(u.Path) == len(prefix) {
		return nil, denied
	}
	return u, nil
}

// DownloadFile streams a bounded hosted file to a caller-owned writer, without redirects.
func (c *Client) DownloadFile(ctx context.Context, token Token, file FileDetails, workspaceID string, destination io.Writer) (DownloadResult, error) {
	endpoint, err := fileDownloadURL(file, workspaceID)
	if err != nil {
		return DownloadResult{}, err
	}
	if token == "" {
		return DownloadResult{}, errx.New(errx.Auth, "CREDENTIAL_MISSING", "profile credential is missing", "login the exact profile")
	}
	if file.Size < 0 || file.Size > contract.MaxFileDownloadBytes {
		return DownloadResult{}, errx.New(errx.Usage, "FILE_TOO_LARGE", "file exceeds the download bound", "choose a file of at most 250 MiB")
	}
	ctx, cancel := context.WithTimeout(ctx, contract.FileDownloadDeadline)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return DownloadResult{}, errx.New(errx.Internal, "REQUEST_BUILD_FAILED", "failed to build file request", "report this defect")
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := c.do(ctx, token, request, downloadOperation)
	if err != nil {
		return DownloadResult{}, err
	}
	defer func() { _ = response.Body.Close() }() // Read outcome is handled below.
	encoding := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding")))
	if (encoding != "" && encoding != "identity") || (response.ContentLength >= 0 && response.ContentLength != file.Size) {
		return DownloadResult{}, unsupportedFileContent()
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type")) // Missing/invalid types do not establish HTML permission.
	expectedType, _, _ := mime.ParseMediaType(file.Mimetype)
	if contentType == "text/html" && expectedType != "text/html" {
		return DownloadResult{}, unsupportedFileContent()
	}
	digest := sha256.New()
	// Read at most the declared size plus one; never buffer a video in memory.
	sink := &downloadWriter{destination: destination}
	count, err := io.Copy(io.MultiWriter(sink, digest), io.LimitReader(response.Body, file.Size+1))
	if sink.failed {
		return DownloadResult{}, errx.New(errx.Internal, "FILE_OUTPUT_FAILED", "cannot write download file", "check local storage and permissions before retrying")
	}
	if err != nil {
		return DownloadResult{}, networkError(readOperation, errx.StageResponseBody, errors.New("file transfer failed"))
	}
	if count != file.Size {
		return DownloadResult{}, unsupportedFileContent()
	}
	return DownloadResult{Bytes: count, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

// downloadWriter distinguishes destination failures, including short writes, from network reads.
type downloadWriter struct {
	destination io.Writer
	failed      bool
}

func (w *downloadWriter) Write(p []byte) (int, error) {
	n, err := w.destination.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.failed = err != nil
	return n, err
}

func unsupportedFileContent() error {
	return errx.New(errx.Internal, "FILE_CONTENT_UNSUPPORTED", "Slack returned unsupported file content or metadata", "check file metadata and credential permissions; do not retry unchanged")
}
