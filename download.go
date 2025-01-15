package k6provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

// DownloadConfig defines the configuration for downloading files
type DownloadConfig struct {
	// AuthType type of passed in the header "Authorization: <type> <auth>".
	// Can be used to set the type as "Basic", "Token" or any custom type. Default to "Bearer"
	AuthType string
	// Authorization contain authorization credentials for download requests
	// Passed in the "Authorization <type> <credentials" (see AuthType for the meaning of <type>)
	// If not specified the value of K6_DOWNLOAD_AUTH is used.
	// If no value is defined, the Authentication header is not passed (except is passed as a custom header
	// see Headers)
	Authorization string
	// DownloadHeaders HTTP headers for the download requests
	Headers map[string]string
	// ProxyURL URL to proxy for downloading binaries
	ProxyURL string
}

// downloader is a utility for downloading files
type downloader struct {
	client   *http.Client
	auth     string
	authType string
	headers  map[string]string
}

// newDownloader returns a new Downloader
func newDownloader(config DownloadConfig) (*downloader, error) {
	httpClient := http.DefaultClient

	proxyURL := config.ProxyURL
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

	downloadAuth := config.Authorization
	if downloadAuth == "" {
		downloadAuth = os.Getenv("K6_DOWNLOAD_AUTH")
	}

	downloadAuthType := config.AuthType
	if downloadAuthType == "" {
		downloadAuthType = "Bearer"
	}

	return &downloader{
		client:   httpClient,
		auth:     downloadAuth,
		authType: downloadAuthType,
		headers:  config.Headers,
	}, nil
}

func (d *downloader) download(ctx context.Context, from string, dest io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, from, nil)
	if err != nil {
		return err
	}

	// add authorization header "Authorization: <type> <auth>"
	if d.auth != "" {
		req.Header.Add("Authorization", fmt.Sprintf("%s %s", d.authType, d.auth))
	}

	// add custom headers
	for h, v := range d.headers {
		req.Header.Add(h, v)
	}

	resp, err := d.client.Do(req)
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
