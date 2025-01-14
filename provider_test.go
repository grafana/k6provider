package k6provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/grafana/k6build/pkg/builder"
	"github.com/grafana/k6build/pkg/catalog"
	"github.com/grafana/k6build/pkg/server"
	"github.com/grafana/k6build/pkg/store/client"
	filestore "github.com/grafana/k6build/pkg/store/file"
	storesrv "github.com/grafana/k6build/pkg/store/server"
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

func Test_Provider(t *testing.T) { //nolint:paralleltest
	// 1. create local file store
	store, err := filestore.NewFileStore(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatalf("store setup %v", err)
	}
	storeConfig := storesrv.StoreServerConfig{
		Store: store,
	}

	// 2. start an object store server
	storeSrv := httptest.NewServer(storesrv.NewStoreServer(storeConfig))

	// 3. start a download proxy
	storeURL, _ := url.Parse(storeSrv.URL)
	proxyHandler := httputil.NewSingleHostReverseProxy(storeURL)
	proxy := httptest.NewServer(proxyHandler)
	defer proxy.Close()

	// 4. configure a local builder
	storeClient, err := client.NewStoreClient(client.StoreClientConfig{Server: storeSrv.URL})
	if err != nil {
		t.Fatalf("store client setup %v", err)
	}
	catalog, err := catalog.NewCatalog(context.TODO(), "testdata/catalog.json")
	if err != nil {
		t.Fatalf("build server setup %v", err)
	}
	buildConfig := builder.Config{
		Opts: builder.Opts{
			GoOpts: builder.GoOpts{
				CopyGoEnv: true,
			},
		},
		Catalog: catalog,
		Store:   storeClient,
	}
	builder, err := builder.New(context.TODO(), buildConfig)
	if err != nil {
		t.Fatalf("setup %v", err)
	}

	// 5. start a builder server
	srvConfig := server.APIServerConfig{
		BuildService: builder,
	}
	buildSrv := httptest.NewServer(server.NewAPIServer(srvConfig))

	testCases := []struct {
		title      string
		opts       *k6deps.Options
		buildProxy http.HandlerFunc
		config     Config
		expectErr  error
		expect     string
	}{
		{
			title:  "build k6 from env variable",
			config: Config{},
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
		},
		{
			title: "test download using proxy",
			config: Config{
				DownloadProxyURL: proxy.URL,
			},
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
		},
		{
			title: "test proxy unavailable",
			config: Config{
				DownloadProxyURL: "http://127.0.0.1:12345",
			},
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
			expectErr: ErrDownload,
		},
		{
			title: "test authentication using bearer token",
			config: Config{
				BuildServiceAuth: "token",
			},
			buildProxy: newAuthorizationProxy(buildSrv.URL, "Authorization", "Bearer token"),
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
			buildProxy: newAuthorizationProxy(buildSrv.URL, "Custom-Auth", "token"),
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
			buildProxy: newAuthorizationProxy(buildSrv.URL, "Authorization", "Bearer token"),
			opts: &k6deps.Options{
				Env: k6deps.Source{Name: "K6_DEPS", Contents: []byte("k6=v0.50.0")},
			},
			expectErr: ErrBuild,
		},
	}

	for _, tc := range testCases { //nolint:paralleltest
		t.Run(tc.title, func(t *testing.T) {
			// by default, we use the build service, but if there's a
			// proxy defined, we use it
			testSrvURL := buildSrv.URL
			if tc.buildProxy != nil {
				testSrv := httptest.NewServer(tc.buildProxy)
				defer testSrv.Close()
				testSrvURL = testSrv.URL
			}

			config := tc.config
			config.BuildServiceURL = testSrvURL
			config.BinDir = filepath.Join(t.TempDir(), "provider")

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
