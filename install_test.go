package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	os.Stdout = w
	defer func() { os.Stdout = orig }()
	defer r.Close()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			done <- fmt.Sprintf("copy error: %v", err)
			return
		}

		done <- buf.String()
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}

	return <-done
}

func TestDownload(t *testing.T) {
	want := []byte("hello from server")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(want)
	}))
	defer srv.Close()

	tmp, err := download(srv.URL, int64(len(want)), 1<<20)
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

func TestDownloadAddsAuthorizationForAllowedGitHubHosts(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret-token")

	setTestHTTPTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer secret-token")
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}))

	tmp, err := download("https://github.com/owner/repo/releases/download/v1.2.3/tool.tar.gz", 2, 1<<20)
	if err != nil {
		t.Fatalf("download: unexpected error: %v", err)
	}

	defer os.Remove(tmp.Name())
	defer tmp.Close()
}

func TestDownloadSkipsAuthorizationForUntrustedHosts(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret-token")

	tests := []string{
		"https://example.com/tool.tar.gz",
		"http://github.com/owner/repo/releases/download/v1.2.3/tool.tar.gz",
	}

	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			setTestHTTPTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if got := req.Header.Get("Authorization"); got != "" {
					t.Fatalf("Authorization = %q, want empty", got)
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("ok")),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}))

			tmp, err := download(rawURL, 2, 1<<20)
			if err != nil {
				t.Fatalf("download: unexpected error: %v", err)
			}

			defer os.Remove(tmp.Name())
			defer tmp.Close()
		})
	}
}

func TestDownloadTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setTestHTTPTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}))

		_, err := download("http://example.com/dl", 0, 1<<20)
		if err == nil {
			t.Fatal("download expected timeout error")
		}

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("download timeout error = %v, want %v", err, context.DeadlineExceeded)
		}
	})
}

func TestDownloadRejectsDeclaredAssetSizeOverLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called when asset metadata already exceeds the limit")
	}))
	defer srv.Close()

	_, err := download(srv.URL, 10, 5)
	if err == nil {
		t.Fatal("download expected error for oversized asset metadata")
	}

	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDownloadRejectsResponseBodyOverLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("123456"))
	}))
	defer srv.Close()

	_, err := download(srv.URL, 0, 5)
	if err == nil {
		t.Fatal("download expected error for oversized response body")
	}

	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInstallBinary(t *testing.T) {
	tmpDir := t.TempDir()

	content := []byte("binary content")
	src, err := writeTempFile(bytes.NewReader(content), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer os.Remove(src.Name())
	defer src.Close()

	linkPath, err := installBinary(tmpDir, "owner", "repo", "v1.0.0", "tool", src)
	if err != nil {
		t.Fatalf("installBinary: %v", err)
	}

	binPath := filepath.Join(tmpDir, "ghinst", "owner", "repo@"+encodeTagForPath("v1.0.0"), "tool")
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

func TestInstallBinaryEncodesSlashyTagsAndListDisplaysDecodedVersion(t *testing.T) {
	tmpDir := t.TempDir()
	tag := "release/2026 build"

	src, err := writeTempFile(bytes.NewReader([]byte("binary content")), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer os.Remove(src.Name())
	defer src.Close()

	linkPath, err := installBinary(tmpDir, "owner", "repo", tag, "tool", src)
	if err != nil {
		t.Fatalf("installBinary: %v", err)
	}

	encodedDir := filepath.Join(tmpDir, "ghinst", "owner", "repo@"+encodeTagForPath(tag))
	if _, err := os.Stat(filepath.Join(encodedDir, "tool")); err != nil {
		t.Fatalf("encoded install path missing binary: %v", err)
	}

	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}

	if target != filepath.Join(encodedDir, "tool") {
		t.Fatalf("symlink target = %q, want %q", target, filepath.Join(encodedDir, "tool"))
	}

	out := captureStdout(t, func() {
		if err := listInstalled(tmpDir); err != nil {
			t.Fatalf("listInstalled: %v", err)
		}
	})

	want := "* owner/repo release/2026 build\n"
	if out != want {
		t.Fatalf("listInstalled output mismatch\n got:\n%q\nwant:\n%q", out, want)
	}
}

func TestInstallBinaryRejectsSymlinkedManagedRoot(t *testing.T) {
	tmpDir := t.TempDir()
	external := t.TempDir()

	if err := os.Symlink(external, filepath.Join(tmpDir, "ghinst")); err != nil {
		t.Fatalf("Symlink ghinst root: %v", err)
	}

	src, err := writeTempFile(bytes.NewReader([]byte("binary content")), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer os.Remove(src.Name())
	defer src.Close()

	if _, err := installBinary(tmpDir, "owner", "repo", "v1.0.0", "tool", src); err == nil {
		t.Fatal("installBinary expected error for symlinked managed root")
	}

	entries, err := os.ReadDir(external)
	if err != nil {
		t.Fatalf("ReadDir external: %v", err)
	}

	if len(entries) != 0 {
		t.Fatalf("external directory should remain untouched, got %d entries", len(entries))
	}
}

func TestInstallBinaryReplacesRunningBinaryLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-specific behavior")
	}

	tmpDir := t.TempDir()
	owner, repo, tag, binName := "owner", "repo", "v1.0.0", "tool"

	initial := []byte("#!/bin/sh\nsleep 2\n")
	src1, err := writeTempFile(bytes.NewReader(initial), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile initial: %v", err)
	}

	defer os.Remove(src1.Name())
	defer src1.Close()

	if _, err := installBinary(tmpDir, owner, repo, tag, binName, src1); err != nil {
		t.Fatalf("installBinary initial: %v", err)
	}

	binPath := filepath.Join(tmpDir, "ghinst", owner, repo+"@"+encodeTagForPath(tag), binName)
	cmd := exec.Command(binPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start running binary: %v", err)
	}

	defer cmd.Wait()
	if cmd.Process == nil {
		t.Fatal("running process should have a PID")
	}

	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("running process is not alive: %v", err)
	}

	replacement := []byte("#!/bin/sh\necho replaced\n")
	src2, err := writeTempFile(bytes.NewReader(replacement), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile replacement: %v", err)
	}

	defer os.Remove(src2.Name())
	defer src2.Close()

	if _, err := installBinary(tmpDir, owner, repo, tag, binName, src2); err != nil {
		t.Fatalf("installBinary replace while running: %v", err)
	}

	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("ReadFile replaced binary: %v", err)
	}

	if !bytes.Equal(got, replacement) {
		t.Fatalf("replaced binary content mismatch")
	}

	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("Stat replaced binary: %v", err)
	}

	if info.Mode()&0111 == 0 {
		t.Error("replaced binary is not executable")
	}
}

func TestInstallBinaryCleanupOnRenameFailure(t *testing.T) {
	tmpDir := t.TempDir()
	owner, repo, tag, binName := "owner", "repo", "v1.0.0", "tool"

	installDir := filepath.Join(tmpDir, "ghinst", owner, repo+"@"+encodeTagForPath(tag))
	if err := os.MkdirAll(filepath.Join(installDir, binName), 0755); err != nil {
		t.Fatalf("setup installDir: %v", err)
	}

	src, err := writeTempFile(bytes.NewReader([]byte("binary content")), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer os.Remove(src.Name())
	defer src.Close()

	if _, err := installBinary(tmpDir, owner, repo, tag, binName, src); err == nil {
		t.Fatal("installBinary expected error when rename target is a directory")
	}

	if _, err := os.Stat(installDir); err != nil {
		t.Fatalf("installDir should remain on failure when it pre-existed, stat err=%v", err)
	}

	if info, err := os.Stat(filepath.Join(installDir, binName)); err != nil || !info.IsDir() {
		t.Fatalf("pre-existing install content should be preserved, stat err=%v info=%v", err, info)
	}

	tmpMatches, err := filepath.Glob(filepath.Join(tmpDir, "ghinst", owner, repo+"@"+encodeTagForPath(tag), ".tmp-*"))
	if err != nil {
		t.Fatalf("Glob temp files: %v", err)
	}

	if len(tmpMatches) != 0 {
		t.Fatalf("temp files were not cleaned up: %v", tmpMatches)
	}
}

func TestInstallBinaryRefusesReplacingRegularFileInBin(t *testing.T) {
	tmpDir := t.TempDir()

	src, err := writeTempFile(bytes.NewReader([]byte("binary content")), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer os.Remove(src.Name())
	defer src.Close()

	linkDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(linkDir, 0755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}

	linkPath := filepath.Join(linkDir, "tool")
	if err := os.WriteFile(linkPath, []byte("keep me"), 0644); err != nil {
		t.Fatalf("WriteFile existing linkPath: %v", err)
	}

	if _, err := installBinary(tmpDir, "owner", "repo", "v1.0.0", "tool", src); err == nil {
		t.Fatal("installBinary expected error when bin path is regular file")
	}

	got, err := os.ReadFile(linkPath)
	if err != nil {
		t.Fatalf("ReadFile existing linkPath: %v", err)
	}

	if !bytes.Equal(got, []byte("keep me")) {
		t.Fatalf("existing regular file at link path should be preserved")
	}
}

func TestInstallBinaryPreservesExistingSymlinkWhenReplacementSetupFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions for symlink creation are not reliable on Windows")
	}

	tmpDir := t.TempDir()

	src, err := writeTempFile(bytes.NewReader([]byte("binary content")), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer os.Remove(src.Name())
	defer src.Close()

	linkDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(linkDir, 0755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}

	oldTarget := filepath.Join(tmpDir, "existing-tool")
	if err := os.WriteFile(oldTarget, []byte("keep me"), 0755); err != nil {
		t.Fatalf("WriteFile old target: %v", err)
	}

	linkPath := filepath.Join(linkDir, "tool")
	if err := os.Symlink(oldTarget, linkPath); err != nil {
		t.Fatalf("Symlink existing linkPath: %v", err)
	}

	if err := os.Chmod(linkDir, 0555); err != nil {
		t.Fatalf("Chmod bin dir: %v", err)
	}
	defer os.Chmod(linkDir, 0755)

	if _, err := installBinary(tmpDir, "owner", "repo", "v1.0.0", "tool", src); err == nil {
		t.Fatal("installBinary expected error when temporary symlink cannot be created")
	}

	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink existing linkPath: %v", err)
	}

	if target != oldTarget {
		t.Fatalf("existing symlink target = %q, want %q", target, oldTarget)
	}
}

func TestListInstalledMarksActiveVersions(t *testing.T) {
	tmpDir := t.TempDir()

	dirs := []string{
		filepath.Join(tmpDir, "ghinst", "owner", "repo@v1.0.0"),
		filepath.Join(tmpDir, "ghinst", "owner", "repo@v2.0.0"),
		filepath.Join(tmpDir, "ghinst", "owner2", "tool@v3.0.0"),
		filepath.Join(tmpDir, "bin"),
	}

	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("MkdirAll %s: %v", d, err)
		}
	}

	activeBin := filepath.Join(tmpDir, "ghinst", "owner", "repo@v2.0.0", "repo")
	if err := os.WriteFile(activeBin, []byte("bin"), 0755); err != nil {
		t.Fatalf("WriteFile active binary: %v", err)
	}

	if err := os.Symlink(activeBin, filepath.Join(tmpDir, "bin", "repo")); err != nil {
		t.Fatalf("Symlink active binary: %v", err)
	}

	out := captureStdout(t, func() {
		if err := listInstalled(tmpDir); err != nil {
			t.Fatalf("listInstalled: %v", err)
		}
	})

	want := strings.Join([]string{
		"* owner/repo v2.0.0",
		"  owner/repo v1.0.0",
		"  owner2/tool v3.0.0",
		"",
	}, "\n")
	if out != want {
		t.Fatalf("listInstalled output mismatch\n got:\n%q\nwant:\n%q", out, want)
	}
}

func TestListInstalledMissingGhinstDirIsNoOp(t *testing.T) {
	tmpDir := t.TempDir()

	out := captureStdout(t, func() {
		if err := listInstalled(tmpDir); err != nil {
			t.Fatalf("listInstalled: %v", err)
		}
	})
	if out != "" {
		t.Fatalf("expected no output, got %q", out)
	}
}

func TestListInstalledReturnsErrorWhenBinPathIsNotDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "bin"), []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile bin: %v", err)
	}

	if err := listInstalled(tmpDir); err == nil {
		t.Fatal("listInstalled expected error when bin path is not a directory")
	}
}

func TestPurge(t *testing.T) {
	tmpDir := t.TempDir()
	ownerDir := filepath.Join(tmpDir, "ghinst", "owner")

	v1 := filepath.Join(ownerDir, "repo@v1.0.0")
	v2 := filepath.Join(ownerDir, "repo@v2.0.0")

	for _, d := range []string{v1, v2} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate v2 being the currently linked version.
	binPath := filepath.Join(v2, "repo")
	if err := os.WriteFile(binPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(binPath, filepath.Join(binDir, "repo")); err != nil {
		t.Fatal(err)
	}

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

func TestPurgeOtherRepoLinkedFirst(t *testing.T) {
	tmpDir := t.TempDir()
	ownerDir := filepath.Join(tmpDir, "ghinst", "owner")

	v1 := filepath.Join(ownerDir, "repo@v1.0.0")
	v2 := filepath.Join(ownerDir, "repo@v2.0.0")
	other := filepath.Join(ownerDir, "other@v1.0.0")

	for _, d := range []string{v1, v2, other} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Link "other" so it appears first alphabetically in bin/.
	otherBin := filepath.Join(other, "other")
	if err := os.WriteFile(otherBin, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(otherBin, filepath.Join(binDir, "other")); err != nil {
		t.Fatal(err)
	}

	// Link v2 of "repo".
	repoBin := filepath.Join(v2, "repo")
	if err := os.WriteFile(repoBin, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(repoBin, filepath.Join(binDir, "repo")); err != nil {
		t.Fatal(err)
	}

	if err := purge(tmpDir, "owner", "repo"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if _, err := os.Stat(v1); !os.IsNotExist(err) {
		t.Error("v1.0.0 should have been purged")
	}

	if _, err := os.Stat(v2); err != nil {
		t.Error("v2.0.0 (linked) should remain")
	}

	if _, err := os.Stat(other); err != nil {
		t.Error("other@v1.0.0 should remain untouched")
	}
}

func TestPurgeReturnsErrorWhenBinDirMissingAndMultipleVersionsExist(t *testing.T) {
	tmpDir := t.TempDir()
	ownerDir := filepath.Join(tmpDir, "ghinst", "owner")

	v1 := filepath.Join(ownerDir, "repo@v1.0.0")
	v2 := filepath.Join(ownerDir, "repo@v2.0.0")
	for _, d := range []string{v1, v2} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	err := purge(tmpDir, "owner", "repo")
	if err == nil {
		t.Fatal("purge expected error when active version cannot be determined")
	}

	if !strings.Contains(err.Error(), "could not determine active version for owner/repo") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(v1); err != nil {
		t.Fatalf("v1.0.0 should remain after failed purge: %v", err)
	}

	if _, err := os.Stat(v2); err != nil {
		t.Fatalf("v2.0.0 should remain after failed purge: %v", err)
	}
}

func TestPurgeReturnsErrorWhenNoMatchingSymlinkExists(t *testing.T) {
	tmpDir := t.TempDir()
	ownerDir := filepath.Join(tmpDir, "ghinst", "owner")

	v1 := filepath.Join(ownerDir, "repo@v1.0.0")
	v2 := filepath.Join(ownerDir, "repo@v2.0.0")
	other := filepath.Join(ownerDir, "other@v1.0.0")
	for _, d := range []string{v1, v2, other} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}

	otherBin := filepath.Join(other, "other")
	if err := os.WriteFile(otherBin, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(otherBin, filepath.Join(binDir, "other")); err != nil {
		t.Fatal(err)
	}

	err := purge(tmpDir, "owner", "repo")
	if err == nil {
		t.Fatal("purge expected error when no matching symlink exists")
	}

	if !strings.Contains(err.Error(), "could not determine active version for owner/repo") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(v1); err != nil {
		t.Fatalf("v1.0.0 should remain after failed purge: %v", err)
	}

	if _, err := os.Stat(v2); err != nil {
		t.Fatalf("v2.0.0 should remain after failed purge: %v", err)
	}
}

func TestPurgeMissingOwnerDirIsNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	if err := purge(tmpDir, "owner", "repo"); err != nil {
		t.Fatalf("purge missing owner dir should be no-op: %v", err)
	}
}

func TestPurgeRejectsSymlinkedOwnerDir(t *testing.T) {
	tmpDir := t.TempDir()
	external := t.TempDir()

	for _, dir := range []string{
		filepath.Join(external, "repo@"+encodeTagForPath("v1.0.0")),
		filepath.Join(external, "repo@"+encodeTagForPath("v2.0.0")),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}

	if err := os.MkdirAll(filepath.Join(tmpDir, "ghinst"), 0755); err != nil {
		t.Fatalf("MkdirAll ghinst root: %v", err)
	}

	if err := os.Symlink(external, filepath.Join(tmpDir, "ghinst", "owner")); err != nil {
		t.Fatalf("Symlink owner dir: %v", err)
	}

	err := purge(tmpDir, "owner", "repo")
	if err == nil {
		t.Fatal("purge expected error for symlinked owner dir")
	}

	if !strings.Contains(err.Error(), "refusing to use symlinked path") {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, dir := range []string{
		filepath.Join(external, "repo@"+encodeTagForPath("v1.0.0")),
		filepath.Join(external, "repo@"+encodeTagForPath("v2.0.0")),
	} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("expected external dir %s to remain: %v", dir, err)
		}
	}
}

func TestCopyToTempFileRejectsOversizedContent(t *testing.T) {
	tmp, err := copyToTempFile("", "ghinst-test-*", strings.NewReader("123456"), 5)
	if err == nil {
		tmp.Close()
		os.Remove(tmp.Name())
		t.Fatal("copyToTempFile expected size limit error")
	}

	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInstallDirParts(t *testing.T) {
	repo, encodedTag, ok := installDirParts("repo@" + encodeTagForPath("release/2026 build"))
	if !ok {
		t.Fatal("installDirParts should parse managed install dir names")
	}

	if repo != "repo" {
		t.Fatalf("repo = %q, want %q", repo, "repo")
	}

	if decodeTagFromPathComponent(encodedTag) != "release/2026 build" {
		t.Fatalf("decoded tag = %q, want %q", decodeTagFromPathComponent(encodedTag), "release/2026 build")
	}
}

func TestActiveInstallDirs(t *testing.T) {
	tmpDir := t.TempDir()
	installDir := filepath.Join(tmpDir, "ghinst", "owner", "repo@"+encodeTagForPath("v1.0.0"))
	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatalf("MkdirAll install dir: %v", err)
	}

	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll bin dir: %v", err)
	}

	binPath := filepath.Join(installDir, "tool")
	if err := os.WriteFile(binPath, []byte("bin"), 0755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}

	if err := os.Symlink(binPath, filepath.Join(binDir, "tool")); err != nil {
		t.Fatalf("Symlink tool: %v", err)
	}

	active, err := activeInstallDirs(tmpDir)
	if err != nil {
		t.Fatalf("activeInstallDirs: %v", err)
	}

	if !active[installDir] {
		t.Fatalf("activeInstallDirs missing %q", installDir)
	}
}
