package k6provider

import (
	"context"
	"crypto/rand"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/grafana/k6build/pkg/testutils"
	"github.com/grafana/k6deps"
)

// checks request has the correct Authorization header
func newAuthorizationProxy(buildSrv string, header string, authorization string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(header) != authorization {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		url, _ := url.Parse(buildSrv)
		httputil.NewSingleHostReverseProxy(url).ServeHTTP(w, r)
	}
}

// Pass through requests
func newTransparentProxy(upstream string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url, _ := url.Parse(upstream)
		httputil.NewSingleHostReverseProxy(url).ServeHTTP(w, r)
	}
}

// fail with the given error up to a number of times
func newUnreliableProxy(upstream string, status int, failures int) http.HandlerFunc {
	requests := 0
	return func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests <= failures {
			w.WriteHeader(status)
			return
		}

		url, _ := url.Parse(upstream)
		httputil.NewSingleHostReverseProxy(url).ServeHTTP(w, r)
	}
}

// returns a corrupted random content
func newCorruptedProxy() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		buffer := make([]byte, 1024)
		_, _ = rand.Read(buffer)
		_, _ = w.Write(buffer)
	}
}

func Test_Provider(t *testing.T) { //nolint:tparallel
	t.Parallel()

	testEnv, err := testutils.NewTestEnv(
		testutils.TestEnvConfig{
			WorkDir:    t.TempDir(),
			CatalogURL: "testdata/catalog.json",
		},
	)
	if err != nil {
		t.Fatalf("test env setup %v", err)
	}
	t.Cleanup(testEnv.Cleanup)

	testCases := []struct {
		title         string
		opts          *k6deps.Options
		buildProxy    http.HandlerFunc
		downloadProxy http.HandlerFunc
		config        Config
		expectErr     error
		expect        string
	}{
		{
			title:  "build k6 from env variable",
			config: Config{},
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
		},
		{
			title: "test authentication using bearer token",
			config: Config{
				BuildServiceAuth: "token",
			},
			buildProxy: newAuthorizationProxy(testEnv.BuildServiceURL(), "Authorization", "Bearer token"),
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
			expectErr: nil,
		},
		{
			title: "test authentication using custom header",
			config: Config{
				BuildServiceHeaders: map[string]string{
					"Custom-Auth": "token",
				},
			},
			buildProxy: newAuthorizationProxy(testEnv.BuildServiceURL(), "Custom-Auth", "token"),
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
			expectErr: nil,
		},
		{
			title: "test authentication failed (missing bearer token)",
			config: Config{
				BuildServiceAuth: "",
			},
			buildProxy: newAuthorizationProxy(testEnv.BuildServiceURL(), "Authorization", "Bearer token"),
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
			expectErr: ErrBuild,
		},
		{
			title:         "test download using proxy",
			downloadProxy: newTransparentProxy(testEnv.StoreServiceURL()),
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
		},
		{
			title: "test download proxy unavailable",
			config: Config{
				DownloadConfig: DownloadConfig{
					ProxyURL: "http://127.0.0.1:12345",
				},
			},
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
			expectErr: ErrDownload,
		},
		{
			title: "test download authentication using bearer token",
			config: Config{
				BuildServiceAuth: "token",
				DownloadConfig: DownloadConfig{
					Authorization: "token",
				},
			},
			downloadProxy: newAuthorizationProxy(testEnv.StoreServiceURL(), "Authorization", "Bearer token"),
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
			expectErr: nil,
		},
		{
			title: "test download authentication failed (missing bearer token)",
			config: Config{
				DownloadConfig: DownloadConfig{
					Authorization: "",
				},
			},
			downloadProxy: newAuthorizationProxy(testEnv.StoreServiceURL(), "Authorization", "Bearer token"),
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
			expectErr: ErrDownload,
		},
		{
			title:         "test download with default retries",
			downloadProxy: newUnreliableProxy(testEnv.StoreServiceURL(), http.StatusServiceUnavailable, 1),
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
		},
		{
			title:         "test we don't retry forever",
			config:        Config{DownloadConfig: DownloadConfig{Retries: 1}},
			downloadProxy: newUnreliableProxy(testEnv.StoreServiceURL(), http.StatusServiceUnavailable, math.MaxInt),
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
			expectErr: ErrDownload,
		},
		{
			title:         "detect corrupted binary",
			downloadProxy: newCorruptedProxy(),
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
			expectErr: ErrDownload,
		},
	}

	for _, tc := range testCases { //nolint:paralleltest
		t.Run(tc.title, func(t *testing.T) {
			// by default, we use the build service, but if there's a
			// proxy defined, we use it
			testSrvURL := testEnv.BuildServiceURL()
			if tc.buildProxy != nil {
				testSrv := httptest.NewServer(tc.buildProxy)
				defer testSrv.Close()
				testSrvURL = testSrv.URL
			}

			// if there's a download proxy, we use it
			testStoreProxy := ""
			if tc.downloadProxy != nil {
				downloadProxy := httptest.NewServer(tc.downloadProxy)
				defer downloadProxy.Close()
				testStoreProxy = downloadProxy.URL
			}

			config := tc.config
			config.BinDir = filepath.Join(t.TempDir(), "provider")
			config.BuildServiceURL = testSrvURL
			// FIXME: override download proxy if not set in the test. This is needed to test wrong proxy URL
			if config.DownloadConfig.ProxyURL == "" {
				config.DownloadConfig.ProxyURL = testStoreProxy
			}

			provider, err := NewProvider(config)
			if err != nil {
				t.Fatalf("initializing provider %v", err)
			}

			deps, err := k6deps.Analyze(tc.opts)
			if err != nil {
				t.Fatalf("analyzing dependencies %v", err)
			}

			k6, err := provider.GetBinary(context.TODO(), deps)
			if !errors.Is(err, tc.expectErr) {
				t.Fatalf("expected %v got %v", tc.expectErr, err)
			}

			if err != nil {
				return
			}

			cmd := exec.Command(k6.Path, "version")

			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("running command %v", err)
			}

			t.Log(string(out))
		})
	}
}
