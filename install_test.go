package main

import (
	"bytes"
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

func TestInstallBinaryReplacesRunningBinaryLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-specific behavior")
	}

	tmpDir := t.TempDir()
	owner, repo, tag, binName := "owner", "repo", "v1.0.0", "tool"

	initial := []byte("#!/bin/sh\nsleep 2\n")
	src1, err := writeTempFile(bytes.NewReader(initial))
	if err != nil {
		t.Fatalf("writeTempFile initial: %v", err)
	}
	defer os.Remove(src1.Name())
	defer src1.Close()

	if _, err := installBinary(tmpDir, owner, repo, tag, binName, src1); err != nil {
		t.Fatalf("installBinary initial: %v", err)
	}

	binPath := filepath.Join(tmpDir, "ghinst", owner, repo+"@"+tag, binName)
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
	src2, err := writeTempFile(bytes.NewReader(replacement))
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

	installDir := filepath.Join(tmpDir, "ghinst", owner, repo+"@"+tag)
	if err := os.MkdirAll(filepath.Join(installDir, binName), 0755); err != nil {
		t.Fatalf("setup installDir: %v", err)
	}

	src, err := writeTempFile(bytes.NewReader([]byte("binary content")))
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

	tmpMatches, err := filepath.Glob(filepath.Join(tmpDir, "ghinst", owner, repo+"@"+tag, ".tmp-*"))
	if err != nil {
		t.Fatalf("Glob temp files: %v", err)
	}
	if len(tmpMatches) != 0 {
		t.Fatalf("temp files were not cleaned up: %v", tmpMatches)
	}
}

func TestInstallBinaryRefusesReplacingRegularFileInBin(t *testing.T) {
	tmpDir := t.TempDir()

	src, err := writeTempFile(bytes.NewReader([]byte("binary content")))
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
		"  owner/repo v1.0.0",
		"* owner/repo v2.0.0",
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

func TestPurgeMissingOwnerDirIsNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	if err := purge(tmpDir, "owner", "repo"); err != nil {
		t.Fatalf("purge missing owner dir should be no-op: %v", err)
	}
}
