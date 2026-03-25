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

	var warnings bytes.Buffer
	if err := verifyAssetDigest(Asset{Name: "tool.tar.gz"}, f, &warnings); err != nil {
		t.Fatalf("verifyAssetDigest: unexpected error: %v", err)
	}

	if got := warnings.String(); !strings.Contains(got, "warning: no checksum available") {
		t.Fatalf("warning output = %q, want missing-checksum warning", got)
	}
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
	if err := verifyAssetDigest(asset, f, &bytes.Buffer{}); err != nil {
		t.Fatalf("verifyAssetDigest: unexpected error: %v", err)
	}

	got, err := readAllAndRewind(f)
	if err != nil {
		t.Fatalf("readAllAndRewind: %v", err)
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
	}, f, &bytes.Buffer{})
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
	}, f, &bytes.Buffer{})
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
	}, f, &bytes.Buffer{})
	if err == nil {
		t.Fatal("verifyAssetDigest expected invalid digest error")
	}

	if !strings.Contains(err.Error(), "invalid asset digest") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func readAllAndRewind(f *os.File) ([]byte, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	_, err = f.Seek(0, 0)
	return data, err
}
