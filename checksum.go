package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"
)

func verifyAssetDigest(asset Asset, r io.Reader, warnings io.Writer) error {
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

	if err := hashReader(r, h); err != nil {
		return err
	}

	got := h.Sum(nil)
	if !bytes.Equal(got, want) {
		return fmt.Errorf("checksum mismatch for %s", asset.Name)
	}

	return nil
}

func hashReader(r io.Reader, h hash.Hash) error {
	h.Reset()
	if _, err := io.Copy(h, r); err != nil {
		return err
	}

	return nil
}
