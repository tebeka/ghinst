package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
		{"nodash", "", "", "", true},
		{"/repo", "", "", "", true},
		{"owner/", "", "", "", true},
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
}
