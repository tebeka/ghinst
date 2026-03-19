package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	installDirPreExisted := true
	if _, statErr := os.Stat(installDir); os.IsNotExist(statErr) {
		installDirPreExisted = false
	} else if statErr != nil {
		return "", statErr
	}
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return "", err
	}
	defer func() {
		if err != nil && !installDirPreExisted {
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
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}

	if err := os.Rename(tmpName, binPath); err != nil {
		os.Remove(tmpName)
		return "", err
	}

	// Touch the install dir so purge can sort by most recently installed.
	now := time.Now()
	if err := os.Chtimes(installDir, now, now); err != nil {
		return "", err
	}

	linkDir := filepath.Join(baseDir, "bin")
	if err := os.MkdirAll(linkDir, 0755); err != nil {
		return "", err
	}

	linkPath := filepath.Join(linkDir, binName)
	if info, err := os.Lstat(linkPath); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return "", fmt.Errorf("refusing to replace non-symlink %s", linkPath)
		}
		if err := os.Remove(linkPath); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
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
		return ""
	}

	return filepath.Join(home, ".local")
}

func listInstalled(baseDir string) error {
	active := map[string]bool{}
	binDir := filepath.Join(baseDir, "bin")
	if links, err := os.ReadDir(binDir); err == nil {
		for _, l := range links {
			linkPath := filepath.Join(binDir, l.Name())
			target, err := os.Readlink(linkPath)
			if err == nil {
				active[filepath.Dir(target)] = true
			}
		}
	}

	ghinstDir := filepath.Join(baseDir, "ghinst")
	owners, err := os.ReadDir(ghinstDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, owner := range owners {
		if !owner.IsDir() {
			continue
		}
		ownerDir := filepath.Join(ghinstDir, owner.Name())
		entries, err := os.ReadDir(ownerDir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			repo, version, found := strings.Cut(e.Name(), "@")
			if !found {
				continue
			}
			marker := " "
			if active[filepath.Join(ownerDir, e.Name())] {
				marker = "*"
			}
			fmt.Printf("%s %s/%s %s\n", marker, owner.Name(), repo, version)
		}
	}
	return nil
}

// purge removes all but the currently linked version of owner/repo.
func purge(baseDir, owner, repo string) error {
	ownerDir := filepath.Join(baseDir, "ghinst", owner)

	entries, err := os.ReadDir(ownerDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
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

	// Find the currently linked version by resolving symlinks in <baseDir>/bin/.
	active := ""
	binDir := filepath.Join(baseDir, "bin")
	if links, err := os.ReadDir(binDir); err == nil {
		for _, l := range links {
			target, err := os.Readlink(filepath.Join(binDir, l.Name()))
			if err != nil {
				continue
			}
			dir := filepath.Dir(target)
			if filepath.Dir(dir) == ownerDir {
				active = filepath.Base(dir)
				break
			}
		}
	}

	for _, v := range versions {
		if v.Name() == active {
			continue
		}
		dir := filepath.Join(ownerDir, v.Name())
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		fmt.Printf("purged %s/%s\n", owner, v.Name())
	}

	return nil
}
