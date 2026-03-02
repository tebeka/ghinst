package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

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

	if _, err := os.Stat(installDir); !os.IsNotExist(err) {
		t.Fatalf("installDir should be removed on failure, stat err=%v", err)
	}

	tmpMatches, err := filepath.Glob(filepath.Join(tmpDir, "ghinst", owner, repo+"@"+tag, ".tmp-*"))
	if err != nil {
		t.Fatalf("Glob temp files: %v", err)
	}
	if len(tmpMatches) != 0 {
		t.Fatalf("temp files were not cleaned up: %v", tmpMatches)
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
