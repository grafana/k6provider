package k6provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

const (
	defaultAuthType = "Bearer"
	buildPath       = "build"
)

// buildService defines the interface for building custom k6 binaries
type buildService interface {
	Build(ctx context.Context, platform string, k6Constraints string, deps []dependency) (buildArtifact, error)
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
	K6Constraints string        `json:"k6,omitempty"`
	Dependencies  []dependency  `json:"dependencies,omitempty"`
	Platform      string        `json:"platform,omitempty"`
}

// buildResponse defines the response for a BuildRequest
type buildResponse struct {
	Error    *WrappedError `json:"error,omitempty"`
	Artifact buildArtifact `json:"artifact,omitempty"`
}

// buildServiceClientConfig defines the configuration for accessing a remote build service
type buildServiceClientConfig struct {
	URL               string
	Authorization     string
	AuthorizationType string
	Headers           map[string]string
	HTTPClient        *http.Client
}

// buildClient implements buildService via HTTP
type buildClient struct {
	srvURL   *url.URL
	auth     string
	authType string
	headers  map[string]string
	client   *http.Client
}

func newBuildServiceClient(config buildServiceClientConfig) (buildService, error) {
	if config.URL == "" {
		return nil, NewWrappedError(ErrConfig, fmt.Errorf("build service URL is required"))
	}

	srvURL, err := url.Parse(config.URL)
	if err != nil {
		return nil, NewWrappedError(ErrConfig, fmt.Errorf("invalid server URL: %w", err))
	}

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	authType := config.AuthorizationType
	if authType == "" {
		authType = defaultAuthType
	}

	return &buildClient{
		srvURL:   srvURL,
		auth:     config.Authorization,
		authType: authType,
		headers:  config.Headers,
		client:   client,
	}, nil
}

func (r *buildClient) Build(
	ctx context.Context,
	platform string,
	k6Constraints string,
	deps []dependency,
) (buildArtifact, error) {
	req := buildRequest{
		Platform:     platform,
		K6Constraints: k6Constraints,
		Dependencies: deps,
	}

	var resp buildResponse
	if err := r.doRequest(ctx, buildPath, &req, &resp); err != nil {
		if resp.Error != nil {
			return buildArtifact{}, resp.Error
		}
		return buildArtifact{}, err
	}

	if resp.Error != nil {
		return buildArtifact{}, resp.Error
	}

	return resp.Artifact, nil
}

func (r *buildClient) doRequest(ctx context.Context, path string, request, response any) error {
	marshaled := &bytes.Buffer{}
	if err := json.NewEncoder(marshaled).Encode(request); err != nil {
		return NewWrappedError(ErrBuild, err)
	}

	reqURL := r.srvURL.JoinPath(path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL.String(), marshaled)
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

	resp, err := r.client.Do(req)
	if err != nil {
		return NewWrappedError(ErrBuild, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return NewWrappedError(ErrBuild, fmt.Errorf("status %s", resp.Status))
	}

	if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
		return NewWrappedError(ErrBuild, err)
	}

	return nil
}

