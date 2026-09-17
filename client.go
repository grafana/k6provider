package k6provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const (
	defaultAuthType = "Bearer"
	buildPath       = "build"
	// DefaultBuildRetries number of retries for build requests
	DefaultBuildRetries = 3
)

// StatusError indicates a build service request failed with a non-200 HTTP status.
// Exposing the status code (instead of only a formatted string) lets callers, such as
// the retry logic below, distinguish transient upstream failures (502/503/504).
type StatusError struct {
	StatusCode int
	Status     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("status %s", e.Status)
}

// retryableBuildStatus reports whether an HTTP status from the build service
// indicates a transient failure worth retrying.
func retryableBuildStatus(code int) bool {
	switch code {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// dependency defines a dependency and its semantic version constraints
type dependency struct {
	Name        string `json:"name,omitempty"`
	Constraints string `json:"constraints,omitempty"`
}

// buildArtifact is the artifact returned by the build service (internal representation)
type buildArtifact struct {
	ID           string            `json:"id,omitempty"`
	URL          string            `json:"url,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
	Platform     string            `json:"platform,omitempty"`
	Checksum     string            `json:"checksum,omitempty"`
}

// buildRequest defines a request to the build service
type buildRequest struct {
	K6ModPath     string       `json:"k6_mod_path,omitempty"`
	K6Constraints string       `json:"k6,omitempty"`
	Dependencies  []dependency `json:"dependencies,omitempty"`
	Platform      string       `json:"platform,omitempty"`
}

// buildResponse defines the response for a BuildRequest
type buildResponse struct {
	Error    *WrappedError `json:"error,omitempty"`
	Artifact buildArtifact `json:"artifact"`
}

// buildClient builds custom k6 binaries via HTTP
type buildClient struct {
	srvURL   *url.URL
	auth     string
	authType string
	headers  map[string]string
	retries  int
	backoff  time.Duration
	logger   *slog.Logger
}

func newBuildServiceClient(
	urlStr, authorization, authorizationType string, headers map[string]string,
	retries int, backoff time.Duration, logger *slog.Logger,
) (*buildClient, error) {
	srvURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, NewWrappedError(ErrConfig, fmt.Errorf("invalid server URL: %w", err))
	}

	authType := authorizationType
	if authType == "" {
		authType = defaultAuthType
	}

	if retries < 0 {
		return nil, NewWrappedError(ErrConfig, fmt.Errorf("build service retries cannot be negative"))
	}
	if retries == 0 {
		retries = DefaultBuildRetries
	}
	if backoff < 0 {
		return nil, NewWrappedError(ErrConfig, fmt.Errorf("build service backoff cannot be negative"))
	}
	if backoff == 0 {
		backoff = DefaultBackoff
	}

	return &buildClient{
		srvURL:   srvURL,
		auth:     authorization,
		authType: authType,
		headers:  headers,
		retries:  retries,
		backoff:  backoff,
		logger:   logger,
	}, nil
}

func (r *buildClient) Build(
	ctx context.Context,
	platform string,
	k6ModPath string,
	k6Constraints string,
	deps []dependency,
) (buildArtifact, error) {
	req := buildRequest{
		K6ModPath:     k6ModPath,
		Platform:      platform,
		K6Constraints: k6Constraints,
		Dependencies:  deps,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return buildArtifact{}, NewWrappedError(ErrBuild, err)
	}

	var resp buildResponse
	if err := r.doRequestWithRetry(ctx, buildPath, body, &resp); err != nil {
		return buildArtifact{}, err
	}
	if resp.Error != nil {
		return buildArtifact{}, resp.Error
	}
	return resp.Artifact, nil
}

// doRequestWithRetry calls doRequest, retrying with exponential backoff if the
// build service responds with a transient status (502/503/504). It gives up
// early if ctx is done, so a request that is already timing out upstream
// doesn't keep sleeping past its caller's deadline.
func (r *buildClient) doRequestWithRetry(ctx context.Context, path string, body []byte, response any) error {
	backoff := r.backoff

	var lastErr error
	for attempt := 0; attempt <= r.retries; attempt++ {
		if attempt > 0 {
			r.logger.Debug("Build request retry",
				"attempt", attempt,
				"backoff", backoff,
				"error", lastErr,
			)

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return NewWrappedError(ErrBuild, ctx.Err())
			}
			backoff *= 2
		}

		lastErr = r.doRequest(ctx, path, body, response)
		if lastErr == nil {
			return nil
		}

		var statusErr *StatusError
		if !errors.As(lastErr, &statusErr) || !retryableBuildStatus(statusErr.StatusCode) {
			return lastErr
		}
	}

	return lastErr
}

func (r *buildClient) doRequest(ctx context.Context, path string, body []byte, response any) error {
	reqURL := r.srvURL.JoinPath(path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL.String(), bytes.NewReader(body))
	if err != nil {
		return NewWrappedError(ErrBuild, err)
	}
	req.Header.Set("Content-Type", "application/json")

	if r.auth != "" {
		req.Header.Set("Authorization", fmt.Sprintf("%s %s", r.authType, r.auth))
	}

	for h, v := range r.headers {
		req.Header.Set(h, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return NewWrappedError(ErrBuild, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return NewWrappedError(ErrBuild, &StatusError{StatusCode: resp.StatusCode, Status: resp.Status})
	}

	if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
		return NewWrappedError(ErrBuild, err)
	}

	return nil
}
