// Package k6provider implements a library for providing custom k6 binaries
// using a k6build service
package k6provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/grafana/k6build"
	"github.com/grafana/k6build/pkg/client"
	"github.com/grafana/k6deps"
)

const (
	k6Binary             = "k6"
	k6Module             = "k6"
	defaultPruneInterval = time.Hour
	defaultAuthType      = "Bearer"
)

var (
	// ErrBinary indicates an error creating local binary
	ErrBinary = errors.New("creating binary")
	// ErrBuild indicates an error building binary
	ErrBuild = errors.New("building binary")
	// ErrConfig is produced by invalid configuration
	ErrConfig = errors.New("invalid configuration")
	// ErrDownload indicates an error downloading binary
	ErrDownload = errors.New("downloading binary")
	// ErrInvalidParameters is produced by invalid build parameters
	ErrInvalidParameters = errors.New("invalid build parameters")
	// ErrPruningCache indicates an error pruning the binary cache
	ErrPruningCache = errors.New("pruning cache")
)

// WrappedError defines a custom error type that allows creating an error
// specifying its cause.
//
// This type is compatible with the error interface.
//
// Contrary to the error wrapping mechanism provided by the standard library
// the cause can be extracted using the unwrap() method.
//
// WrappedError also implements the Is method to that it can compare to an error
// based on the result of the Error() method, overcoming a limitation of the error
// implemented in the stdlib.
//
//	Example:
//	var (
//	    err    = errors.New("error")
//	    root   = errors.New("root cause")
//	    cause  = NewWrappedError(cause, root)
//	    ferr   = fmt.Errorf("%w %w", err, cause)
//	    werr   = NewWrappedError(err,)
//	    target = errors.New("error")
//	)
//
//	errors.Is(werr, err)    // returns true
//	errors.Is(werr, cause)  // returns true
//	errors.Is(werr, root)   // return true
//	errors.Is(err, target)  // returns false (err != target)
//	errors.Is(werr, target) // returns true  (err.Error() == target.Error())
//	ferr.Unwrap()           // return nil
//	werr.Unwrap()           // return cause
//	werr.Unwrap().Unwrap()  // return root
type WrappedError = *k6build.WrappedError

// NewWrappedError return a new [WrappedError] from an error and its reason
func NewWrappedError(err error, reason error) WrappedError {
	return k6build.NewWrappedError(err, reason)
}

// AsWrappedError returns and error as a [WrapperError] and a boolean indicating if it was possible
func AsWrappedError(err error) (WrappedError, bool) {
	buildErr := &k6build.WrappedError{}
	if !errors.As(err, &buildErr) {
		return nil, false
	}
	return buildErr, true
}

// K6Binary defines the attributes of a k6 binary
type K6Binary struct {
	// Path to the binary
	Path string
	// Dependencies as a map of name: version
	// e.g. {"k6": "v0.50.0", "k6/x/kubernetes": "v0.9.0"}
	Dependencies map[string]string
	// Checksum of the binary
	Checksum string
}

// UnmarshalDeps returns the dependencies as a list of name:version pairs separated by ";"
func (b K6Binary) UnmarshalDeps() string {
	buffer := &bytes.Buffer{}
	for dep, version := range b.Dependencies {
		buffer.WriteString(fmt.Sprintf("%s:%q;", dep, version))
	}
	return buffer.String()
}

// Config defines the configuration of the Provider.
type Config struct {
	// Platform for the binaries. Defaults to the current platform
	Platform string
	// BinDir path to binary directory. Defaults to the os' tmp dir
	BinDir string
	// BuildServiceURL URL of the k6 build service
	// If not specified the value from K6_BUILD_SERVICE_URL environment variable is used
	BuildServiceURL string
	// BuildServiceAuthType type of passed in the header "Authorization: <type> <auth>".
	// Can be used to set the type as "Basic", "Token" or any custom type. Default to "Bearer"
	BuildServiceAuthType string
	// BuildServiceAuth contain authorization credentials for BuildService requests
	// Passed in the "Authorization <type> <credentials" (see BuildServiceAuthType for the meaning of <type>)
	// If not specified the value of K6_BUILD_SERVICE_AUTH is used.
	// If no value is defined, the Authentication header is not passed (except is passed as a custom header
	// see BuildServiceHeaders)
	BuildServiceAuth string
	// BuildServiceHeaders HTTP headers for the k6 build service
	BuildServiceHeaders map[string]string
	// DownloadProxyURL URL to proxy for downloading binaries
	DownloadProxyURL string
	// HighWaterMark is the upper limit of cache size to trigger a prune
	HighWaterMark int64
	// PruneInterval minimum time between prune attempts. Defaults to 1h
	PruneInterval time.Duration
}

// Provider implements an interface for providing custom k6 binaries
// from a [k6build] service.
//
// [k6build]: https://github.com/grafana/k6build
type Provider struct {
	client   *http.Client
	binDir   string
	buildSrv k6build.BuildService
	platform string
	pruner   *Pruner
}

// NewDefaultProvider returns a Provider with default settings
//
// Expects the K6_BUILD_SERVICE_URL environment variable to be set
// with the URL to the k6build service
func NewDefaultProvider() (*Provider, error) {
	return NewProvider(Config{})
}

// NewProvider returns a [Provider] with the given Options
//
// If BuildServiceURL is not set, it will use the K6_BUILD_SERVICE_URL environment variable
// If DownloadProxyURL is not set, it will use the K6_DOWNLOAD_PROXY environment variable
func NewProvider(config Config) (*Provider, error) {
	binDir := config.BinDir
	if binDir == "" {
		binDir = filepath.Join(os.TempDir(), "k6provider", "cache")
	}

	httpClient := http.DefaultClient

	proxyURL := config.DownloadProxyURL
	if proxyURL == "" {
		proxyURL = os.Getenv("K6_DOWNLOAD_PROXY")
	}
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, NewWrappedError(ErrConfig, err)
		}
		proxy := http.ProxyURL(parsed)
		transport := &http.Transport{Proxy: proxy}
		httpClient = &http.Client{Transport: transport}
	}

	buildSrvURL := config.BuildServiceURL
	if buildSrvURL == "" {
		buildSrvURL = os.Getenv("K6_BUILD_SERVICE_URL")
	}
	if buildSrvURL == "" {
		return nil, NewWrappedError(ErrConfig, fmt.Errorf("build service URL is required"))
	}

	buildSrvAuth := config.BuildServiceAuth
	if buildSrvAuth == "" {
		buildSrvAuth = os.Getenv("K6_BUILD_SERVICE_AUTH")
	}

	buildSrvAuthType := config.BuildServiceAuthType
	if buildSrvAuthType == "" && buildSrvAuth != "" {
		buildSrvAuthType = "Bearer"
	}

	buildSrv, err := client.NewBuildServiceClient(
		client.BuildServiceClientConfig{
			URL:               buildSrvURL,
			Authorization:     buildSrvAuth,
			AuthorizationType: buildSrvAuthType,
			Headers:           config.BuildServiceHeaders,
		},
	)
	if err != nil {
		return nil, NewWrappedError(ErrConfig, err)
	}

	platform := config.Platform
	if platform == "" {
		platform = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	}

	pruneInterval := config.PruneInterval
	if config.HighWaterMark > 0 && pruneInterval == 0 {
		pruneInterval = defaultPruneInterval
	}

	return &Provider{
		client:   httpClient,
		binDir:   binDir,
		buildSrv: buildSrv,
		platform: platform,
		pruner:   NewPruner(binDir, config.HighWaterMark, pruneInterval),
	}, nil
}

// GetArtifact returns a custom k6 artifact that satisfies the given a set of dependencies.
// from the configured build service.
// it's useful if you want to get the artifact without downloading the binary.
func (p *Provider) GetArtifact(
	ctx context.Context,
	deps k6deps.Dependencies,
) (k6build.Artifact, error) {
	k6Constrains, buildDeps := buildDeps(deps)

	artifact, err := p.buildSrv.Build(ctx, p.platform, k6Constrains, buildDeps)
	if err != nil {
		if !errors.Is(err, ErrInvalidParameters) {
			return k6build.Artifact{}, NewWrappedError(ErrBuild, err)
		}

		// it is an invalid build parameters, we are interested in the
		// root cause
		cause := errors.Unwrap(err)
		for errors.Unwrap(cause) != nil {
			cause = errors.Unwrap(cause)
		}
		return k6build.Artifact{}, NewWrappedError(ErrInvalidParameters, cause)
	}

	return artifact, nil
}

// GetBinary returns a custom k6 binary that satisfies the given a set of dependencies.
//
// If the k6 version constrains are not specified, "*" is used as default.
//
// If the binary for the given dependencies does not exist, it will be built
// using the configured build service and stored in the cache directory.
//
// If the binary exists, it will be returned from the cache.
//
// The returned K6Binary has the path to the custom k6 binary, the list of
// dependencies and the checksum of the binary.
//
// If any error occurs while building, downloading or checking the binary,
// an [WrappedError] will be returned. This error will be one of the errors
// defined in the k6provider packaged. Using errors.Unwrap will return its cause.
func (p *Provider) GetBinary(
	ctx context.Context,
	deps k6deps.Dependencies,
) (K6Binary, error) {
	artifact, err := p.GetArtifact(ctx, deps)
	if err != nil {
		return K6Binary{}, err
	}

	artifactDir := filepath.Join(p.binDir, artifact.ID)
	binPath := filepath.Join(artifactDir, k6Binary)
	_, err = os.Stat(binPath)

	// binary already exists
	if err == nil {
		go p.pruner.Touch(binPath)

		return K6Binary{
			Path:         binPath,
			Dependencies: artifact.Dependencies,
			Checksum:     artifact.Checksum,
		}, nil
	}

	// other error
	if !os.IsNotExist(err) {
		return K6Binary{}, NewWrappedError(ErrBinary, err)
	}

	// binary doesn't exists
	err = os.MkdirAll(artifactDir, 0o700)
	if err != nil {
		return K6Binary{}, NewWrappedError(ErrBinary, err)
	}

	target, err := os.OpenFile( //nolint:gosec
		binPath,
		os.O_WRONLY|os.O_CREATE,
		syscall.S_IRUSR|syscall.S_IXUSR|syscall.S_IWUSR,
	)
	if err != nil {
		return K6Binary{}, NewWrappedError(ErrBinary, err)
	}

	err = p.download(ctx, artifact.URL, target)
	if err != nil {
		_ = os.RemoveAll(artifactDir)
		return K6Binary{}, NewWrappedError(ErrDownload, err)
	}

	_ = target.Close()

	// start pruning in background
	// TODO: handle case the calling process is cancelled
	go p.pruner.Prune() //nolint:errcheck

	return K6Binary{
		Path:         binPath,
		Dependencies: artifact.Dependencies,
		Checksum:     artifact.Checksum,
	}, nil
}

func (p *Provider) download(ctx context.Context, from string, dest io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, from, nil)
	if err != nil {
		return err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
	}

	defer resp.Body.Close() //nolint:errcheck

	_, err = io.Copy(dest, resp.Body)

	return err
}

// buildDeps takes a set of k6 dependencies and returns a string representing
// the version constraints for the k6 and a slice of k6build.Dependencies
// representing the extension dependencies. The default k6 constrain is "*".
func buildDeps(deps k6deps.Dependencies) (string, []k6build.Dependency) {
	bdeps := make([]k6build.Dependency, 0, len(deps))
	k6constraint := "*"

	for _, dep := range deps {
		if dep.Name == k6Module {
			k6constraint = dep.GetConstraints().String()
			continue
		}

		bdeps = append(
			bdeps,
			k6build.Dependency{
				Name:        dep.Name,
				Constraints: dep.GetConstraints().String(),
			},
		)
	}

	return k6constraint, bdeps
}
