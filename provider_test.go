package k6provider

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"

	fileCache "github.com/grafana/k6build/pkg/cache/file"
	cachesrv "github.com/grafana/k6build/pkg/cache/server"
	"github.com/grafana/k6build/pkg/local"
	"github.com/grafana/k6build/pkg/server"
	"github.com/grafana/k6deps"
)

func Test_Provider(t *testing.T) { //nolint:paralleltest
	// 1. create local file cache
	cache, err := fileCache.NewFileCache(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatalf("cache setup %v", err)
	}
	cacheConfig := cachesrv.CacheServerConfig{
		Cache: cache,
	}

	// 2. start a cache server
	cacheSrv := httptest.NewServer(cachesrv.NewCacheServer(cacheConfig))
	buildConfig := local.BuildServiceConfig{
		Catalog:   "testdata/catalog.json",
		CopyGoEnv: true,
		CacheURL:  cacheSrv.URL,
	}

	// 3. start a download proxy
	cacheURL, _ := url.Parse(cacheSrv.URL)
	proxyHandler := httputil.NewSingleHostReverseProxy(cacheURL)
	proxy := httptest.NewServer(proxyHandler)
	defer proxy.Close()

	// 4. configure a local builder
	builder, err := local.NewBuildService(context.TODO(), buildConfig)
	if err != nil {
		t.Fatalf("setup %v", err)
	}

	// 5. start a builder server
	srvConfig := server.APIServerConfig{
		BuildService: builder,
	}
	buildSrv := httptest.NewServer(server.NewAPIServer(srvConfig))

	testCases := []struct {
		title     string
		opts      *k6deps.Options
		config    Config
		expectErr error
		expect    string
	}{
		{
			title: "build k6 from env variable",
			config: Config{
				BinDir:          filepath.Join(t.TempDir(), "provider"),
				BuildServiceURL: buildSrv.URL,
			},
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
		},
		{
			title: "test download using proxy",
			config: Config{
				BinDir:           filepath.Join(t.TempDir(), "provider"),
				BuildServiceURL:  buildSrv.URL,
				DownloadProxyURL: proxy.URL,
			},
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
		},
		{
			title: "test proxy unavailable",
			config: Config{
				BinDir:           filepath.Join(t.TempDir(), "provider"),
				BuildServiceURL:  buildSrv.URL,
				DownloadProxyURL: "http://127.0.0.1:12345",
			},
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
			expectErr: ErrDownload,
		},
	}

	for _, tc := range testCases { //nolint:paralleltest
		t.Run(tc.title, func(t *testing.T) {
			provider, err := NewProvider(tc.config)
			if err != nil {
				t.Fatalf("initializing provider %v", err)
			}

			deps, err := k6deps.Analyze(tc.opts)
			if err != nil {
				t.Fatalf("analizing dependencies %v", err)
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
