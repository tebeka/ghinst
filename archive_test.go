package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"testing"
)

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
