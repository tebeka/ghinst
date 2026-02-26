package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

var osAliases = map[string][]string{
	"linux":   {"linux"},
	"darwin":  {"darwin", "macos", "osx"},
	"windows": {"windows", "win"},
}

var archAliases = map[string][]string{
	"amd64": {"amd64", "x86_64"},
	"arm64": {"arm64", "aarch64"},
	"386":   {"386", "i386", "i686"},
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s owner/repo[@version]\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	owner, repo, tag, err := parseTarget(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	release, err := fetchRelease(owner, repo, tag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	asset, err := selectAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fmt.Fprintln(os.Stderr, "available assets:")
		for _, a := range release.Assets {
			fmt.Fprintf(os.Stderr, "  %s\n", a.Name)
		}
		os.Exit(1)
	}

	data, err := download(asset.BrowserDownloadURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: downloading: %v\n", err)
		os.Exit(1)
	}

	binName, binData, err := extractBinary(data, asset.Name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: extracting: %v\n", err)
		os.Exit(1)
	}

	linkPath, err := installBinary(owner, repo, release.TagName, binName, binData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: installing: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("installed %s (%s) → %s\n", repo, release.TagName, linkPath)
}

func download(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// extractBinary extracts the binary from an archive, returning its name and content.
// For non-archives (raw binary), it returns the asset name and data as-is.
func extractBinary(data []byte, assetName string) (string, []byte, error) {
	lower := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return "", nil, err
		}
		defer gz.Close()
		return findInTar(tar.NewReader(gz))

	case strings.HasSuffix(lower, ".tar.bz2"):
		return findInTar(tar.NewReader(bzip2.NewReader(bytes.NewReader(data))))

	case strings.HasSuffix(lower, ".zip"):
		return findInZip(data)
	}

	return assetName, data, nil
}

// findInTar returns the first executable file in a tar archive.
func findInTar(tr *tar.Reader) (string, []byte, error) {
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

		data, err := io.ReadAll(tr)
		if err != nil {
			return "", nil, err
		}

		return filepath.Base(hdr.Name), data, nil
	}

	return "", nil, fmt.Errorf("no executable found in archive")
}

// findInZip returns the first executable file in a zip archive.
// Falls back to the first file without an extension if no exec bits are set.
func findInZip(data []byte) (string, []byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", nil, err
	}

	var fallback *zip.File
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		base := filepath.Base(f.Name)
		isExec := f.Mode()&0111 != 0
		noExt := filepath.Ext(base) == ""

		if isExec || noExt {
			if fallback == nil {
				fallback = f
			}
			if isExec {
				content, err := readZipFile(f)
				if err != nil {
					return "", nil, err
				}
				return base, content, nil
			}
		}
	}

	if fallback != nil {
		content, err := readZipFile(fallback)
		if err != nil {
			return "", nil, err
		}
		return filepath.Base(fallback.Name), content, nil
	}

	return "", nil, fmt.Errorf("no executable found in archive")
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// installBinary places the binary under ~/.local/ghinst/owner/repo@tag/
// and symlinks it into ~/.local/bin/.
func installBinary(owner, repo, tag, binName string, data []byte) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	installDir := filepath.Join(home, ".local", "ghinst", owner, repo+"@"+tag)
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return "", err
	}

	binPath := filepath.Join(installDir, binName)
	if err := os.WriteFile(binPath, data, 0755); err != nil {
		return "", err
	}

	linkDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(linkDir, 0755); err != nil {
		return "", err
	}

	linkPath := filepath.Join(linkDir, binName)
	os.Remove(linkPath) // replace any existing symlink
	if err := os.Symlink(binPath, linkPath); err != nil {
		return "", err
	}

	return linkPath, nil
}

func fetchRelease(owner, repo, tag string) (Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	if tag != "" {
		url = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return Release{}, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return Release{}, fmt.Errorf("release not found for %s/%s@%s", owner, repo, tag)
	}
	if resp.StatusCode != 200 {
		return Release{}, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return Release{}, err
	}

	return release, nil
}

func selectAsset(assets []Asset, goos, goarch string) (Asset, error) {
	osPhrases, ok := osAliases[goos]
	if !ok {
		return Asset{}, fmt.Errorf("unsupported OS: %s", goos)
	}

	archPhrases, ok := archAliases[goarch]
	if !ok {
		return Asset{}, fmt.Errorf("unsupported architecture: %s", goarch)
	}

	var candidates []Asset
	for _, a := range assets {
		lower := strings.ToLower(a.Name)
		if matchesAny(lower, osPhrases) && matchesAny(lower, archPhrases) && isArchive(lower) {
			candidates = append(candidates, a)
		}
	}

	if len(candidates) == 0 {
		return Asset{}, fmt.Errorf("no asset found for %s/%s", goos, goarch)
	}

	// Shortest name wins — naturally excludes .sha256, .sbom, etc.
	sort.Slice(candidates, func(i, j int) bool {
		return len(candidates[i].Name) < len(candidates[j].Name)
	})

	return candidates[0], nil
}

func parseTarget(s string) (owner, repo, tag string, err error) {
	slug, tag, _ := strings.Cut(s, "@")
	parts := strings.SplitN(slug, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("invalid target %q: expected owner/repo[@version]", s)
	}
	return parts[0], parts[1], tag, nil
}

var archiveExts = []string{".tar.gz", ".tgz", ".tar.bz2", ".tar.xz", ".zip"}

func isArchive(name string) bool {
	for _, ext := range archiveExts {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

func matchesAny(s string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
