package main

import (
	"encoding/json"
	"flag"
	"fmt"
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
		fmt.Fprintf(os.Stderr, "usage: %s owner/repo\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	owner, repo, err := parseTarget(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	release, err := fetchLatestRelease(owner, repo)
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

	fmt.Println(asset.BrowserDownloadURL)
}

func fetchLatestRelease(owner, repo string) (Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)

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
		return Release{}, fmt.Errorf("release not found for %s/%s", owner, repo)
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

func parseTarget(s string) (owner, repo string, err error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid target %q: expected owner/repo", s)
	}
	return parts[0], parts[1], nil
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
