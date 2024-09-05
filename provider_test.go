package k6provider

import (
	"context"
	"errors"
	"net/http/httptest"
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

	// 3. configure a local builder
	builder, err := local.NewBuildService(context.TODO(), buildConfig)
	if err != nil {
		t.Fatalf("setup %v", err)
	}

	// 4. start a builder server
	srvConfig := server.APIServerConfig{
		BuildService: builder,
	}
	buildSrv := httptest.NewServer(server.NewAPIServer(srvConfig))

	// 5. configure the provider to use the build service
	config := Config{
		BinDir:          filepath.Join(t.TempDir(), "provider"),
		BuildServiceURL: buildSrv.URL,
	}
	provider, err := NewProvider(config)
	if err != nil {
		t.Fatalf("initializing provider %v", err)
	}

	testCases := []struct {
		title     string
		opts      *k6deps.Options
		expectErr error
		expect    string
	}{
		{
			title: "build k6 from env variable",
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
		},
	}

	for _, tc := range testCases { //nolint:paralleltest
		t.Run(tc.title, func(t *testing.T) {
			deps, err := k6deps.Analyze(tc.opts)
			if err != nil {
				t.Fatalf("analizing dependencies %v", err)
			}

			k6, err := provider.GetBinary(context.TODO(), deps)
			if !errors.Is(tc.expectErr, err) {
				t.Fatalf("expected %v got %v", tc.expectErr, err)
			}
			cmd := exec.Command(k6, "version")

			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("running command %v", err)
			}

			t.Log(string(out))
		})
	}
}
