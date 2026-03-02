package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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
	tmp, err := os.CreateTemp(installDir, ".tmp-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}

	if err := tmp.Chmod(0755); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	tmp.Close()

	if err := os.Rename(tmpName, binPath); err != nil {
		os.Remove(tmpName)
		return "", err
	}

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
