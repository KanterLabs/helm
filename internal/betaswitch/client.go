package betaswitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// UnixClient is the read/write client for the fixed broker socket. It does
// not accept a command, path, or arbitrary URL from any API call.
type UnixClient struct {
	SocketPath       string
	HTTP             *http.Client
	MaxBodyBytes     int64
	MaxResponseBytes int64
}

// NewClient creates a broker client for one absolute Unix socket path.
func NewClient(socketPath string) (*UnixClient, error) {
	if socketPath == "" {
		socketPath = DefaultSocketPath
	}
	if !filepath.IsAbs(socketPath) || strings.ContainsAny(socketPath, "\x00\r\n") {
		return nil, fmt.Errorf("socket path must be absolute and contain no controls")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	client.Transport = &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &UnixClient{
		SocketPath:       socketPath,
		HTTP:             client,
		MaxBodyBytes:     DefaultMaxBodyBytes,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}, nil
}

// NewUnixClient is an explicit constructor alias for callers that prefer the
// transport name.
func NewUnixClient(socketPath string) (*UnixClient, error) { return NewClient(socketPath) }

func (c *UnixClient) client() (*http.Client, error) {
	if c == nil || c.SocketPath == "" || !filepath.IsAbs(c.SocketPath) || strings.ContainsAny(c.SocketPath, "\x00\r\n") {
		return nil, fmt.Errorf("invalid client socket path")
	}
	if c.HTTP == nil {
		return nil, fmt.Errorf("client transport is unavailable")
	}
	return c.HTTP, nil
}

// ListReleases gets the validated retained release list.
func (c *UnixClient) ListReleases(ctx context.Context) (ReleasesResponse, error) {
	var response ReleasesResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/releases", nil, &response); err != nil {
		return ReleasesResponse{}, err
	}
	if response.CurrentSHA != "" && !validSHA(response.CurrentSHA) {
		return ReleasesResponse{}, coded(ErrInternal, errors.New("broker returned invalid current SHA"))
	}
	seen := make(map[string]struct{}, len(response.Releases))
	for _, release := range response.Releases {
		if !validSHA(release.SHA) || release.Ref == "" {
			return ReleasesResponse{}, coded(ErrInternal, errors.New("broker returned invalid release"))
		}
		if _, err := branchFromRef(release.Ref); err != nil {
			return ReleasesResponse{}, coded(ErrInternal, errors.New("broker returned invalid release ref"))
		}
		if _, exists := seen[release.SHA]; exists {
			return ReleasesResponse{}, coded(ErrInternal, errors.New("broker returned duplicate release"))
		}
		seen[release.SHA] = struct{}{}
		if release.Current && release.SHA != response.CurrentSHA {
			return ReleasesResponse{}, coded(ErrInternal, errors.New("broker returned inconsistent current release"))
		}
	}
	return response, nil
}

// Releases is an alias for ListReleases.
func (c *UnixClient) Releases(ctx context.Context) (ReleasesResponse, error) {
	return c.ListReleases(ctx)
}

// RequestSwitch enqueues a switch for a validated release SHA.
func (c *UnixClient) RequestSwitch(ctx context.Context, sha string) (Job, error) {
	if err := validateSHA(sha); err != nil {
		return Job{}, coded(ErrInvalidRequest, err)
	}
	var job Job
	if err := c.doJSON(ctx, http.MethodPost, "/v1/switch", SwitchRequest{SHA: sha}, &job); err != nil {
		return Job{}, err
	}
	if err := validateJob(job, sha); err != nil {
		return Job{}, coded(ErrInternal, err)
	}
	return job, nil
}

// Switch is an alias for RequestSwitch.
func (c *UnixClient) Switch(ctx context.Context, sha string) (Job, error) {
	return c.RequestSwitch(ctx, sha)
}

// GetJob reads one durable job by its exact 32-character lower-hex ID.
func (c *UnixClient) GetJob(ctx context.Context, id string) (Job, error) {
	if err := validateJobID(id); err != nil {
		return Job{}, coded(ErrInvalidRequest, err)
	}
	var job Job
	if err := c.doJSON(ctx, http.MethodGet, "/v1/jobs/"+id, nil, &job); err != nil {
		return Job{}, err
	}
	if err := validateJob(job, ""); err != nil {
		return Job{}, coded(ErrInternal, err)
	}
	if job.ID != id {
		return Job{}, coded(ErrInternal, errors.New("broker returned mismatched job ID"))
	}
	return job, nil
}

// Job is an alias for GetJob.
func (c *UnixClient) Job(ctx context.Context, id string) (Job, error) {
	return c.GetJob(ctx, id)
}

func validateJob(job Job, expectedSHA string) error {
	if !validJobID(job.ID) || !validSHA(job.TargetSHA) || !validJobState(job.State) {
		return errors.New("broker returned invalid job")
	}
	if expectedSHA != "" && job.TargetSHA != expectedSHA {
		return errors.New("broker returned mismatched target SHA")
	}
	if job.Error != "" && !validErrorClass(job.Error) {
		return errors.New("broker returned invalid error class")
	}
	return nil
}

func (c *UnixClient) doJSON(ctx context.Context, method, path string, request any, response any) error {
	httpClient, err := c.client()
	if err != nil {
		return coded(ErrUnavailable, err)
	}
	var body io.Reader
	if request != nil {
		data, err := json.Marshal(request)
		if err != nil {
			return coded(ErrInvalidRequest, err)
		}
		maxBodyBytes := c.MaxBodyBytes
		if maxBodyBytes <= 0 {
			maxBodyBytes = DefaultMaxBodyBytes
		}
		if int64(len(data)) > maxBodyBytes {
			return coded(ErrInvalidRequest, errors.New("request body exceeds limit"))
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, body)
	if err != nil {
		return coded(ErrInvalidRequest, err)
	}
	if request != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return coded(ErrUnavailable, err)
	}
	defer resp.Body.Close()
	maxResponseBytes := c.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return coded(ErrUnavailable, err)
	}
	if int64(len(data)) > maxResponseBytes {
		return coded(ErrInternal, errors.New("broker response exceeds limit"))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return coded(statusCode(resp.StatusCode, data), nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(response); err != nil {
		return coded(ErrInternal, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return coded(ErrInternal, errors.New("broker response contains trailing data"))
	}
	return nil
}

func statusCode(status int, data []byte) ErrorCode {
	var envelope struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &envelope) == nil && validErrorClass(envelope.Error) {
		return ErrorCode(envelope.Error)
	}
	switch status {
	case http.StatusBadRequest:
		return ErrInvalidRequest
	case http.StatusForbidden, http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusConflict:
		return ErrConflict
	case http.StatusServiceUnavailable:
		return ErrUnavailable
	default:
		return ErrInternal
	}
}
