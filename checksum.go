package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
)

func verifyAssetDigest(asset Asset, f *os.File, warnings io.Writer) error {
	if asset.Digest == "" {
		fmt.Fprintf(warnings, "warning: no checksum available for %s; skipping verification\n", asset.Name)
		return nil
	}

	algo, wantHex, found := strings.Cut(asset.Digest, ":")
	if !found || wantHex == "" {
		return fmt.Errorf("invalid asset digest %q", asset.Digest)
	}

	var h hash.Hash
	switch strings.ToLower(algo) {
	case "sha256":
		h = sha256.New()
	default:
		return fmt.Errorf("unsupported asset digest algorithm %q", algo)
	}

	want, err := hex.DecodeString(wantHex)
	if err != nil {
		return fmt.Errorf("invalid asset digest %q: %w", asset.Digest, err)
	}

	if err := hashFile(f, h); err != nil {
		return err
	}

	got := h.Sum(nil)
	if !equalBytes(got, want) {
		return fmt.Errorf("checksum mismatch for %s", asset.Name)
	}

	return nil
}

func hashFile(f *os.File, h hash.Hash) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	h.Reset()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	_, err := f.Seek(0, io.SeekStart)
	return err
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
