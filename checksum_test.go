package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
)

func TestVerifyAssetDigestWarnsWhenMissing(t *testing.T) {
	f, err := writeTempFile(bytes.NewReader([]byte("hello")), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer f.Close()

	warnings := captureStderr(t, func() {
		if err := verifyAssetDigest(Asset{Name: "tool.tar.gz"}, f); err != nil {
			t.Fatalf("verifyAssetDigest: unexpected error: %v", err)
		}
	})

	if got := warnings; !strings.Contains(got, "warning: no checksum available") {
		t.Fatalf("warning output = %q, want missing-checksum warning", got)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	os.Stderr = w
	defer func() { os.Stderr = orig }()
	defer r.Close()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			done <- "copy error: " + err.Error()
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

func TestVerifyAssetDigestAcceptsMatchingSHA256(t *testing.T) {
	data := []byte("hello")
	sum := sha256.Sum256(data)

	f, err := writeTempFile(bytes.NewReader(data), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer f.Close()

	asset := Asset{
		Name:   "tool.tar.gz",
		Digest: "sha256:" + hex.EncodeToString(sum[:]),
	}
	if err := verifyAssetDigest(asset, f); err != nil {
		t.Fatalf("verifyAssetDigest: unexpected error: %v", err)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Fatalf("file contents changed after verification")
	}
}

func TestVerifyAssetDigestRejectsMismatch(t *testing.T) {
	f, err := writeTempFile(bytes.NewReader([]byte("hello")), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer f.Close()

	err = verifyAssetDigest(Asset{
		Name:   "tool.tar.gz",
		Digest: "sha256:" + strings.Repeat("0", 64),
	}, f)
	if err == nil {
		t.Fatal("verifyAssetDigest expected checksum mismatch")
	}

	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyAssetDigestRejectsUnsupportedAlgorithm(t *testing.T) {
	f, err := writeTempFile(bytes.NewReader([]byte("hello")), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer f.Close()

	err = verifyAssetDigest(Asset{
		Name:   "tool.tar.gz",
		Digest: "sha512:" + strings.Repeat("0", 128),
	}, f)
	if err == nil {
		t.Fatal("verifyAssetDigest expected unsupported algorithm error")
	}

	if !strings.Contains(err.Error(), "unsupported asset digest algorithm") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyAssetDigestRejectsInvalidDigestFormat(t *testing.T) {
	f, err := writeTempFile(bytes.NewReader([]byte("hello")), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer f.Close()

	err = verifyAssetDigest(Asset{
		Name:   "tool.tar.gz",
		Digest: "sha256-not-a-real-digest",
	}, f)
	if err == nil {
		t.Fatal("verifyAssetDigest expected invalid digest error")
	}

	if !strings.Contains(err.Error(), "invalid asset digest") {
		t.Fatalf("unexpected error: %v", err)
	}
}
