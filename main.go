package main

import (
	"archive/tar"
	"archive/zip"
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
	"runtime/debug"
	"sort"
	"strings"
	"time"
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

func download(url string) (*os.File, error) {
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

	tmp, err := os.CreateTemp("", "ghinst-*")
	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}

	return tmp, nil
}

// extractBinary extracts the binary from an archive into a temp file.
// For non-archives (raw binary), it returns the input file as-is.
func extractBinary(f *os.File, assetName string) (string, *os.File, error) {
	lower := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		gz, err := gzip.NewReader(f)
		if err != nil {
			return "", nil, err
		}
		defer gz.Close()
		return findInTar(tar.NewReader(gz))

	case strings.HasSuffix(lower, ".tar.bz2"):
		return findInTar(tar.NewReader(bzip2.NewReader(f)))

	case strings.HasSuffix(lower, ".zip"):
		info, err := f.Stat()
		if err != nil {
			return "", nil, err
		}
		return findInZip(f, info.Size())
	}

	return assetName, f, nil
}

// findInTar returns the first executable file in a tar archive as a temp file.
func findInTar(tr *tar.Reader) (string, *os.File, error) {
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

		tmp, err := writeTempFile(tr)
		if err != nil {
			return "", nil, err
		}

		return filepath.Base(hdr.Name), tmp, nil
	}

	return "", nil, fmt.Errorf("no executable found in archive")
}

// findInZip returns the first executable file in a zip archive as a temp file.
// Falls back to the first file without an extension if no exec bits are set.
func findInZip(r io.ReaderAt, size int64) (string, *os.File, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", nil, err
	}

	var best *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}

		base := filepath.Base(f.Name)
		isExec := f.Mode()&0111 != 0
		noExt := filepath.Ext(base) == ""

		if isExec {
			best = f
			break
		}
		if noExt && best == nil {
			best = f
		}
	}

	if best == nil {
		return "", nil, fmt.Errorf("no executable found in archive")
	}

	rc, err := best.Open()
	if err != nil {
		return "", nil, err
	}
	defer rc.Close()

	tmp, err := writeTempFile(rc)
	if err != nil {
		return "", nil, err
	}

	return filepath.Base(best.Name), tmp, nil
}

func writeTempFile(r io.Reader) (*os.File, error) {
	tmp, err := os.CreateTemp("", "ghinst-bin-*")
	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(tmp, r); err != nil {
		os.Remove(tmp.Name())
		tmp.Close()
		return nil, err
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		os.Remove(tmp.Name())
		tmp.Close()
		return nil, err
	}

	return tmp, nil
}

// installBinary places the binary under <baseDir>/ghinst/owner/repo@tag/
// and symlinks it into <baseDir>/bin/.
func installBinary(baseDir, owner, repo, tag, binName string, src *os.File) (_ string, err error) {
	installDir := filepath.Join(baseDir, "ghinst", owner, repo+"@"+tag)
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(installDir)
		}
	}()

	binPath := filepath.Join(installDir, binName)
	dst, err := os.OpenFile(binPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}
	dst.Close()

	// Touch the install dir so purge can sort by most recently installed.
	now := time.Now()
	os.Chtimes(installDir, now, now)

	linkDir := filepath.Join(baseDir, "bin")
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

func defaultBaseDir() string {
	if dir := os.Getenv("GHINST_DIR"); dir != "" {
		return dir
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "/usr/local"
	}

	return filepath.Join(home, ".local")
}

// purge removes all but the most recently installed version of owner/repo.
func purge(baseDir, owner, repo string) error {
	ownerDir := filepath.Join(baseDir, "ghinst", owner)

	entries, err := os.ReadDir(ownerDir)
	if err != nil {
		return err
	}

	var versions []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name, _, found := strings.Cut(e.Name(), "@")
		if found && name == repo {
			versions = append(versions, e)
		}
	}

	if len(versions) <= 1 {
		return nil
	}

	// Sort by modification time, keep the newest.
	sort.Slice(versions, func(i, j int) bool {
		iInfo, _ := versions[i].Info()
		jInfo, _ := versions[j].Info()
		return iInfo.ModTime().Before(jInfo.ModTime())
	})

	for _, v := range versions[:len(versions)-1] {
		dir := filepath.Join(ownerDir, v.Name())
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		fmt.Printf("purged %s/%s\n", owner, v.Name())
	}

	return nil
}

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	return info.Main.Version
}

func main() {
	var showVersion bool
	var doPurge bool
	var baseDir string
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&doPurge, "purge", false, "remove all but the latest installed version of owner/repo")
	flag.StringVar(&baseDir, "dir", defaultBaseDir(), "base install directory (overrides GHINST_DIR)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s owner/repo[@version]\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()

	if showVersion {
		fmt.Println(buildVersion())
		return
	}

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	owner, repo, tag, err := parseTarget(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if doPurge {
		if err := purge(baseDir, owner, repo); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
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

	tmp, err := download(asset.BrowserDownloadURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: downloading: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	binName, binFile, err := extractBinary(tmp, asset.Name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: extracting: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(binFile.Name())
	defer binFile.Close()

	linkPath, err := installBinary(baseDir, owner, repo, release.TagName, binName, binFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: installing: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("installed %s (%s) → %s\n", repo, release.TagName, linkPath)
}
