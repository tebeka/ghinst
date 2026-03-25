package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
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

var archiveExts = []string{".tar.gz", ".tgz", ".tar.bz2", ".tar.xz", ".zip"}

var apiBase = "https://api.github.com"
var httpClient = &http.Client{Timeout: 30 * time.Second}

func fetchRelease(owner, repo, tag string) (Release, error) {
	ownerPath := url.PathEscape(owner)
	repoPath := url.PathEscape(repo)
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/latest", apiBase, ownerPath, repoPath)
	if tag != "" {
		endpoint = fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", apiBase, ownerPath, repoPath, url.PathEscape(tag))
	}

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return Release{}, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return Release{}, err
	}

	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		if tag == "" {
			return Release{}, fmt.Errorf("latest release not found for %s/%s", owner, repo)
		}

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
	if strings.Contains(s, "@") && tag == "" {
		return "", "", "", fmt.Errorf("invalid target %q: empty version after @", s)
	}

	parts := strings.SplitN(slug, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("invalid target %q: expected owner/repo[@version]", s)
	}

	return parts[0], parts[1], tag, nil
}

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
