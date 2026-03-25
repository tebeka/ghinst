package main

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ulikunitz/xz"
)

// extractBinary extracts the binary from an archive into a temp file.
// For non-archives (raw binary), it returns the input file as-is.
func extractBinary(f *os.File, assetName string, maxBytes int64) (string, *os.File, error) {
	lower := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		gz, err := gzip.NewReader(f)
		if err != nil {
			return "", nil, err
		}

		defer gz.Close()
		return findInTar(tar.NewReader(gz), maxBytes)

	case strings.HasSuffix(lower, ".tar.bz2"):
		return findInTar(tar.NewReader(bzip2.NewReader(f)), maxBytes)

	case strings.HasSuffix(lower, ".tar.xz"):
		xzr, err := xz.NewReader(f)
		if err != nil {
			return "", nil, err
		}

		return findInTar(tar.NewReader(xzr), maxBytes)

	case strings.HasSuffix(lower, ".zip"):
		info, err := f.Stat()
		if err != nil {
			return "", nil, err
		}

		return findInZip(f, info.Size(), maxBytes)
	}

	if info, err := f.Stat(); err == nil && info.Size() > maxBytes {
		return "", nil, fmt.Errorf("binary size %d bytes exceeds limit of %d bytes", info.Size(), maxBytes)
	} else if err != nil {
		return "", nil, err
	}

	return assetName, f, nil
}

// findInTar returns the first executable file in a tar archive as a temp file.
func findInTar(tr *tar.Reader, maxBytes int64) (string, *os.File, error) {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			return "", nil, err
		}

		if hdr.Typeflag != tar.TypeReg || hdr.FileInfo().Mode()&0111 == 0 {
			continue
		}

		if hdr.Size > maxBytes {
			return "", nil, fmt.Errorf("binary size %d bytes exceeds limit of %d bytes", hdr.Size, maxBytes)
		}

		tmp, err := writeTempFile(tr, maxBytes)
		if err != nil {
			return "", nil, err
		}

		return filepath.Base(hdr.Name), tmp, nil
	}

	return "", nil, fmt.Errorf("no executable found in archive")
}

// findInZip returns the first executable file in a zip archive as a temp file.
// Falls back to likely executable names if no exec bits are set.
func findInZip(r io.ReaderAt, size int64, maxBytes int64) (string, *os.File, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", nil, err
	}

	var execMatch *zip.File
	var exeFallback *zip.File
	var noExtFallback *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}

		base := filepath.Base(f.Name)
		isExec := f.Mode()&0111 != 0
		ext := strings.ToLower(filepath.Ext(base))

		if isExec {
			execMatch = f
			break
		}

		if ext == ".exe" && exeFallback == nil {
			exeFallback = f
			continue
		}

		if ext == "" && noExtFallback == nil {
			noExtFallback = f
		}
	}

	best := execMatch
	if best == nil {
		best = exeFallback
	}

	if best == nil {
		best = noExtFallback
	}

	if best == nil {
		return "", nil, fmt.Errorf("no executable found in archive")
	}

	if int64(best.UncompressedSize64) > maxBytes {
		return "", nil, fmt.Errorf("binary size %d bytes exceeds limit of %d bytes", best.UncompressedSize64, maxBytes)
	}

	rc, err := best.Open()
	if err != nil {
		return "", nil, err
	}

	defer rc.Close()

	tmp, err := writeTempFile(rc, maxBytes)
	if err != nil {
		return "", nil, err
	}

	return filepath.Base(best.Name), tmp, nil
}

func writeTempFile(r io.Reader, maxBytes int64) (*os.File, error) {
	tmp, err := os.CreateTemp("", "ghinst-bin-*")
	if err != nil {
		return nil, err
	}

	written, err := io.Copy(tmp, io.LimitReader(r, maxBytes+1))
	if err != nil {
		os.Remove(tmp.Name())
		tmp.Close()
		return nil, err
	}

	if written > maxBytes {
		os.Remove(tmp.Name())
		tmp.Close()
		return nil, fmt.Errorf("binary size exceeds limit of %d bytes", maxBytes)
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		os.Remove(tmp.Name())
		tmp.Close()
		return nil, err
	}

	return tmp, nil
}
