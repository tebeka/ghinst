package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

func buildTarGz(files []struct {
	name string
	mode int64
	body []byte
}) ([]byte, error) {
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

		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}

		if _, err := tw.Write(f.body); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	if err := gw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func TestFindInTar(t *testing.T) {
	content := []byte("#!/bin/sh\necho hello")
	data, err := buildTarGz([]struct {
		name string
		mode int64
		body []byte
	}{{"tool", 0755, content}})
	if err != nil {
		t.Fatalf("buildTarGz: %v", err)
	}

	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}

	name, tmp, err := findInTar(tar.NewReader(gr), 1<<20)
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
	data2, err := buildTarGz([]struct {
		name string
		mode int64
		body []byte
	}{{"readme.txt", 0644, []byte("hello")}})
	if err != nil {
		t.Fatalf("buildTarGz no-exec: %v", err)
	}

	gr2, err := gzip.NewReader(bytes.NewReader(data2))
	if err != nil {
		t.Fatalf("gzip.NewReader no-exec: %v", err)
	}

	_, _, err = findInTar(tar.NewReader(gr2), 1<<20)
	if err == nil {
		t.Error("findInTar: expected error for archive with no executables")
	}
}

func buildZip(files []struct {
	name string
	mode os.FileMode
	body []byte
}) ([]byte, error) {
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)

	for _, f := range files {
		fh := &zip.FileHeader{Name: f.name, Method: zip.Store}
		fh.SetMode(f.mode)
		w, err := zw.CreateHeader(fh)
		if err != nil {
			return nil, err
		}

		if _, err := w.Write(f.body); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func buildTarXz(files []struct {
	name string
	mode int64
	body []byte
}) ([]byte, error) {
	buf := &bytes.Buffer{}
	xzw, err := xz.NewWriter(buf)
	if err != nil {
		return nil, err
	}

	tw := tar.NewWriter(xzw)
	for _, f := range files {
		hdr := &tar.Header{
			Name:     f.name,
			Typeflag: tar.TypeReg,
			Mode:     f.mode,
			Size:     int64(len(f.body)),
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}

		if _, err := tw.Write(f.body); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	if err := xzw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func buildTarZst(files []struct {
	name string
	mode int64
	body []byte
}) ([]byte, error) {
	buf := &bytes.Buffer{}
	zw, err := zstd.NewWriter(buf)
	if err != nil {
		return nil, err
	}

	tw := tar.NewWriter(zw)
	for _, f := range files {
		hdr := &tar.Header{
			Name:     f.name,
			Typeflag: tar.TypeReg,
			Mode:     f.mode,
			Size:     int64(len(f.body)),
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}

		if _, err := tw.Write(f.body); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func TestFindInZip(t *testing.T) {
	// Exec bit set → returned.
	data, err := buildZip([]struct {
		name string
		mode os.FileMode
		body []byte
	}{{"tool", 0755, []byte("binary")}})
	if err != nil {
		t.Fatalf("buildZip exec: %v", err)
	}

	name, tmp, err := findInZip(bytes.NewReader(data), int64(len(data)), 1<<20)
	if err != nil {
		t.Fatalf("findInZip exec: unexpected error: %v", err)
	}

	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if name != "tool" {
		t.Errorf("findInZip exec name = %q, want %q", name, "tool")
	}

	// No extension, no exec bit → fallback returned.
	data2, err := buildZip([]struct {
		name string
		mode os.FileMode
		body []byte
	}{{"mytool", 0644, []byte("fallback")}})
	if err != nil {
		t.Fatalf("buildZip fallback noext: %v", err)
	}

	name2, tmp2, err := findInZip(bytes.NewReader(data2), int64(len(data2)), 1<<20)
	if err != nil {
		t.Fatalf("findInZip fallback: unexpected error: %v", err)
	}

	defer os.Remove(tmp2.Name())
	defer tmp2.Close()

	if name2 != "mytool" {
		t.Errorf("findInZip fallback name = %q, want %q", name2, "mytool")
	}

	// Extension + no exec bit → no candidates → error.
	data3, err := buildZip([]struct {
		name string
		mode os.FileMode
		body []byte
	}{{"tool.txt", 0644, []byte("text")}})
	if err != nil {
		t.Fatalf("buildZip no candidates: %v", err)
	}

	_, _, err = findInZip(bytes.NewReader(data3), int64(len(data3)), 1<<20)
	if err == nil {
		t.Error("findInZip: expected error for archive with no candidates")
	}

	data4, err := buildZip([]struct {
		name string
		mode os.FileMode
		body []byte
	}{
		{"README", 0644, []byte("readme")},
		{"tool.exe", 0644, []byte("binary")},
	})
	if err != nil {
		t.Fatalf("buildZip exe fallback: %v", err)
	}

	name4, tmp4, err := findInZip(bytes.NewReader(data4), int64(len(data4)), 1<<20)
	if err != nil {
		t.Fatalf("findInZip exe fallback: unexpected error: %v", err)
	}

	defer os.Remove(tmp4.Name())
	defer tmp4.Close()
	if name4 != "tool.exe" {
		t.Fatalf("findInZip exe fallback name = %q, want %q", name4, "tool.exe")
	}
}

func TestExtractBinaryTarXz(t *testing.T) {
	content := []byte("#!/bin/sh\necho xz")
	data, err := buildTarXz([]struct {
		name string
		mode int64
		body []byte
	}{{"tool", 0755, content}})
	if err != nil {
		t.Fatalf("buildTarXz: %v", err)
	}

	archive, err := writeTempFile(bytes.NewReader(data), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer os.Remove(archive.Name())
	defer archive.Close()

	name, tmp, err := extractBinary(archive, "tool.tar.xz", 1<<20)
	if err != nil {
		t.Fatalf("extractBinary: unexpected error: %v", err)
	}

	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if name != "tool" {
		t.Fatalf("extractBinary name = %q, want %q", name, "tool")
	}

	got, err := io.ReadAll(tmp)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, content) {
		t.Fatalf("extractBinary content mismatch: got %q, want %q", got, content)
	}
}

func TestExtractBinaryTarZst(t *testing.T) {
	content := []byte("#!/bin/sh\necho zstd")
	data, err := buildTarZst([]struct {
		name string
		mode int64
		body []byte
	}{{"tool", 0755, content}})
	if err != nil {
		t.Fatalf("buildTarZst: %v", err)
	}

	archive, err := writeTempFile(bytes.NewReader(data), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer os.Remove(archive.Name())
	defer archive.Close()

	name, tmp, err := extractBinary(archive, "tool.tar.zst", 1<<20)
	if err != nil {
		t.Fatalf("extractBinary: unexpected error: %v", err)
	}

	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if name != "tool" {
		t.Fatalf("extractBinary name = %q, want %q", name, "tool")
	}

	got, err := io.ReadAll(tmp)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, content) {
		t.Fatalf("extractBinary content mismatch: got %q, want %q", got, content)
	}
}

func TestExtractBinaryZst(t *testing.T) {
	content := []byte("#!/bin/sh\necho zstd")
	buf := &bytes.Buffer{}
	zw, err := zstd.NewWriter(buf)
	if err != nil {
		t.Fatalf("zstd.NewWriter: %v", err)
	}

	if _, err := zw.Write(content); err != nil {
		t.Fatalf("zstd.Write: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zstd.Close: %v", err)
	}

	archive, err := writeTempFile(bytes.NewReader(buf.Bytes()), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer os.Remove(archive.Name())
	defer archive.Close()

	name, tmp, err := extractBinary(archive, "tool.zst", 1<<20)
	if err != nil {
		t.Fatalf("extractBinary: unexpected error: %v", err)
	}

	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if name != "tool" {
		t.Fatalf("extractBinary name = %q, want %q", name, "tool")
	}

	got, err := io.ReadAll(tmp)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, content) {
		t.Fatalf("extractBinary content mismatch: got %q, want %q", got, content)
	}
}

func TestExtractBinaryRejectsOversizedRawBinary(t *testing.T) {
	archive, err := writeTempFile(bytes.NewReader([]byte("123456")), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer os.Remove(archive.Name())
	defer archive.Close()

	_, _, err = extractBinary(archive, "tool", 5)
	if err == nil {
		t.Fatal("extractBinary expected error for oversized raw binary")
	}

	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractBinaryRejectsOversizedArchiveMember(t *testing.T) {
	content := []byte("123456")
	data, err := buildTarGz([]struct {
		name string
		mode int64
		body []byte
	}{{"tool", 0755, content}})
	if err != nil {
		t.Fatalf("buildTarGz: %v", err)
	}

	archive, err := writeTempFile(bytes.NewReader(data), 1<<20)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	defer os.Remove(archive.Name())
	defer archive.Close()

	_, _, err = extractBinary(archive, "tool.tar.gz", 5)
	if err == nil {
		t.Fatal("extractBinary expected error for oversized archive member")
	}

	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPickZipBinary(t *testing.T) {
	data, err := buildZip([]struct {
		name string
		mode os.FileMode
		body []byte
	}{
		{"README", 0644, []byte("readme")},
		{"tool.exe", 0644, []byte("exe")},
		{"tool", 0755, []byte("exec")},
	})
	if err != nil {
		t.Fatalf("buildZip: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	best := pickZipBinary(zr.File)
	if best == nil {
		t.Fatal("pickZipBinary returned nil")
	}

	if best.Name != "tool" {
		t.Fatalf("pickZipBinary name = %q, want %q", best.Name, "tool")
	}
}
