package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func setTestHTTPTransport(t *testing.T, transport http.RoundTripper) {
	t.Helper()

	old := httpClient.Transport
	httpClient.Transport = transport
	t.Cleanup(func() {
		httpClient.Transport = old
	})
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		input   string
		owner   string
		repo    string
		tag     string
		wantErr bool
	}{
		{"owner/repo", "owner", "repo", "", false},
		{"owner/repo@v1.2.3", "owner", "repo", "v1.2.3", false},
		{"owner/repo@", "", "", "", true},
		{"nodash", "", "", "", true},
		{"/repo", "", "", "", true},
		{"owner/", "", "", "", true},
		{"../repo", "", "", "", true},
		{"owner/re/po", "", "", "", true},
		{"owner/..", "", "", "", true},
	}

	for _, tc := range tests {
		owner, repo, tag, err := parseTarget(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseTarget(%q) expected error, got nil", tc.input)
			}

			continue
		}

		if err != nil {
			t.Errorf("parseTarget(%q) unexpected error: %v", tc.input, err)
			continue
		}

		if owner != tc.owner || repo != tc.repo || tag != tc.tag {
			t.Errorf("parseTarget(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.input, owner, repo, tag, tc.owner, tc.repo, tc.tag)
		}
	}
}

func TestSelectAsset(t *testing.T) {
	assets := []Asset{
		{Name: "tool_linux_amd64.tar.gz"},
		{Name: "tool_linux_amd64.deb"},
		{Name: "tool_macos_arm64.tar.gz"},
		{Name: "tool_linux_amd64.tar.gz.sha256"},
	}

	tests := []struct {
		goos    string
		goarch  string
		want    string
		wantErr bool
	}{
		{"linux", "amd64", "tool_linux_amd64.tar.gz", false},
		{"darwin", "arm64", "tool_macos_arm64.tar.gz", false},
		{"windows", "amd64", "", true},
	}

	for _, tc := range tests {
		a, err := selectAsset(assets, tc.goos, tc.goarch)
		if tc.wantErr {
			if err == nil {
				t.Errorf("selectAsset(%s/%s) expected error, got nil", tc.goos, tc.goarch)
			}

			continue
		}

		if err != nil {
			t.Errorf("selectAsset(%s/%s) unexpected error: %v", tc.goos, tc.goarch, err)
			continue
		}

		if a.Name != tc.want {
			t.Errorf("selectAsset(%s/%s) = %q, want %q", tc.goos, tc.goarch, a.Name, tc.want)
		}
	}
}

func TestIsArchive(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"tool.tar.gz", true},
		{"tool.tgz", true},
		{"tool.tar.bz2", true},
		{"tool.tar.xz", true},
		{"tool.zip", true},
		{"tool.deb", false},
		{"tool.rpm", false},
		{"tool.exe", false},
		{"tool.sha256", false},
	}

	for _, tc := range tests {
		got := isArchive(tc.name)
		if got != tc.want {
			t.Errorf("isArchive(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFetchRelease(t *testing.T) {
	want := Release{
		TagName: "v1.2.3",
		Assets: []Asset{
			{Name: "tool_linux_amd64.tar.gz", BrowserDownloadURL: "http://example.com/dl"},
		},
	}

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	got, err := fetchRelease("owner", "repo", "")
	if err != nil {
		t.Fatalf("fetchRelease: unexpected error: %v", err)
	}

	if got.TagName != want.TagName {
		t.Errorf("TagName = %q, want %q", got.TagName, want.TagName)
	}

	if len(got.Assets) != 1 || got.Assets[0].Name != want.Assets[0].Name {
		t.Errorf("Assets = %v, want %v", got.Assets, want.Assets)
	}

	if gotPath != "/repos/owner/repo/releases/latest" {
		t.Errorf("request path = %q, want %q", gotPath, "/repos/owner/repo/releases/latest")
	}
}

func TestFetchReleaseEscapesTagPathComponent(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		json.NewEncoder(w).Encode(Release{TagName: "ok"})
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	tag := "release/2026+build"
	if _, err := fetchRelease("owner", "repo", tag); err != nil {
		t.Fatalf("fetchRelease with escaped tag: %v", err)
	}

	wantPath := "/repos/owner/repo/releases/tags/" + url.PathEscape(tag)
	if gotPath != wantPath {
		t.Fatalf("request path = %q, want %q", gotPath, wantPath)
	}
}

func TestFetchReleaseAddsAuthorizationForGitHubAPI(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret-token")
	old := apiBase
	apiBase = "https://api.github.com"
	defer func() { apiBase = old }()

	setTestHTTPTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer secret-token")
		}

		body, err := json.Marshal(Release{TagName: "v1.2.3"})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}))

	if _, err := fetchRelease("owner", "repo", ""); err != nil {
		t.Fatalf("fetchRelease: unexpected error: %v", err)
	}
}

func TestFetchReleaseSkipsAuthorizationForNonGitHubAPIBase(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret-token")
	old := apiBase
	apiBase = "https://example.com"
	defer func() { apiBase = old }()

	setTestHTTPTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty", got)
		}

		body, err := json.Marshal(Release{TagName: "v1.2.3"})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}))

	if _, err := fetchRelease("owner", "repo", ""); err != nil {
		t.Fatalf("fetchRelease: unexpected error: %v", err)
	}
}

func TestFetchReleaseNotFoundMessageForLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	_, err := fetchRelease("owner", "repo", "")
	if err == nil {
		t.Fatal("fetchRelease expected error")
	}

	if !strings.Contains(err.Error(), "latest release not found for owner/repo") {
		t.Fatalf("unexpected error message: %v", err)
	}

	if strings.Contains(err.Error(), "@") {
		t.Fatalf("latest-release not found error should not include @: %v", err)
	}
}

func TestFetchReleaseNotFoundMessageForTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	_, err := fetchRelease("owner", "repo", "v1.2.3")
	if err == nil {
		t.Fatal("fetchRelease expected error")
	}

	if !strings.Contains(err.Error(), "release not found for owner/repo@v1.2.3") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestFetchReleaseServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	_, err := fetchRelease("owner", "repo", "")
	if err == nil {
		t.Fatal("fetchRelease expected non-nil error")
	}

	if !strings.Contains(err.Error(), "GitHub API returned 500") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestFetchReleaseTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setTestHTTPTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}))

		_, err := fetchRelease("owner", "repo", "")
		if err == nil {
			t.Fatal("fetchRelease expected timeout error")
		}

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("fetchRelease timeout error = %v, want context deadline exceeded", err)
		}
	})
}
