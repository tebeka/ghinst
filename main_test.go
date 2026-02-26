package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func buildTarGz(files []struct {
	name string
	mode int64
	body []byte
}) []byte {
	buf := &bytes.Buffer{}
	gw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gw)

	for _, f := range files {
		hdr := &tar.Header{
			Name:     f.name,
			Typeflag: tar.TypeReg,
			Mode:     f.mode,
			Size:     int64(len(f.body)),
		}
		tw.WriteHeader(hdr)
		tw.Write(f.body)
	}

	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestFindInTar(t *testing.T) {
	content := []byte("#!/bin/sh\necho hello")
	data := buildTarGz([]struct {
		name string
		mode int64
		body []byte
	}{{"tool", 0755, content}})

	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}

	name, tmp, err := findInTar(tar.NewReader(gr))
	if err != nil {
		t.Fatalf("findInTar: unexpected error: %v", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if name != "tool" {
		t.Errorf("findInTar name = %q, want %q", name, "tool")
	}

	got, _ := io.ReadAll(tmp)
	if !bytes.Equal(got, content) {
		t.Errorf("findInTar content mismatch: got %q, want %q", got, content)
	}

	// No executables → error.
	data2 := buildTarGz([]struct {
		name string
		mode int64
		body []byte
	}{{"readme.txt", 0644, []byte("hello")}})

	gr2, _ := gzip.NewReader(bytes.NewReader(data2))
	_, _, err = findInTar(tar.NewReader(gr2))
	if err == nil {
		t.Error("findInTar: expected error for archive with no executables")
	}
}

func buildZip(files []struct {
	name string
	mode os.FileMode
	body []byte
}) []byte {
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)

	for _, f := range files {
		fh := &zip.FileHeader{Name: f.name, Method: zip.Store}
		fh.SetMode(f.mode)
		w, _ := zw.CreateHeader(fh)
		w.Write(f.body)
	}

	zw.Close()
	return buf.Bytes()
}

func TestFindInZip(t *testing.T) {
	// Exec bit set → returned.
	data := buildZip([]struct {
		name string
		mode os.FileMode
		body []byte
	}{{"tool", 0755, []byte("binary")}})

	name, tmp, err := findInZip(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("findInZip exec: unexpected error: %v", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if name != "tool" {
		t.Errorf("findInZip exec name = %q, want %q", name, "tool")
	}

	// No extension, no exec bit → fallback returned.
	data2 := buildZip([]struct {
		name string
		mode os.FileMode
		body []byte
	}{{"mytool", 0644, []byte("fallback")}})

	name2, tmp2, err := findInZip(bytes.NewReader(data2), int64(len(data2)))
	if err != nil {
		t.Fatalf("findInZip fallback: unexpected error: %v", err)
	}
	defer os.Remove(tmp2.Name())
	defer tmp2.Close()

	if name2 != "mytool" {
		t.Errorf("findInZip fallback name = %q, want %q", name2, "mytool")
	}

	// Extension + no exec bit → no candidates → error.
	data3 := buildZip([]struct {
		name string
		mode os.FileMode
		body []byte
	}{{"tool.txt", 0644, []byte("text")}})

	_, _, err = findInZip(bytes.NewReader(data3), int64(len(data3)))
	if err == nil {
		t.Error("findInZip: expected error for archive with no candidates")
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

func TestDownload(t *testing.T) {
	want := []byte("hello from server")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(want)
	}))
	defer srv.Close()

	tmp, err := download(srv.URL)
	if err != nil {
		t.Fatalf("download: unexpected error: %v", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	got, err := io.ReadAll(tmp)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("download content = %q, want %q", got, want)
	}
}

func TestInstallBinary(t *testing.T) {
	tmpDir := t.TempDir()

	content := []byte("binary content")
	src, err := writeTempFile(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}
	defer os.Remove(src.Name())
	defer src.Close()

	linkPath, err := installBinary(tmpDir, "owner", "repo", "v1.0.0", "tool", src)
	if err != nil {
		t.Fatalf("installBinary: %v", err)
	}

	binPath := filepath.Join(tmpDir, "ghinst", "owner", "repo@v1.0.0", "tool")
	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("binary not found: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Error("binary is not executable")
	}

	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != binPath {
		t.Errorf("symlink target = %q, want %q", target, binPath)
	}
}

func TestPurge(t *testing.T) {
	tmpDir := t.TempDir()
	ownerDir := filepath.Join(tmpDir, "ghinst", "owner")

	v1 := filepath.Join(ownerDir, "repo@v1.0.0")
	v2 := filepath.Join(ownerDir, "repo@v2.0.0")

	if err := os.MkdirAll(v1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(v2, 0755); err != nil {
		t.Fatal(err)
	}

	t1 := time.Now().Add(-time.Hour)
	t2 := time.Now()
	os.Chtimes(v1, t1, t1)
	os.Chtimes(v2, t2, t2)

	if err := purge(tmpDir, "owner", "repo"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if _, err := os.Stat(v1); !os.IsNotExist(err) {
		t.Error("v1.0.0 should have been purged")
	}
	if _, err := os.Stat(v2); err != nil {
		t.Error("v2.0.0 should remain")
	}

	// Single version → no-op.
	if err := purge(tmpDir, "owner", "repo"); err != nil {
		t.Fatalf("purge single version: %v", err)
	}
	if _, err := os.Stat(v2); err != nil {
		t.Error("v2.0.0 should still remain after no-op purge")
	}
}
