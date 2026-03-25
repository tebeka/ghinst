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

func download(url string, expectedSize, maxBytes int64) (*os.File, error) {
	if expectedSize > 0 && expectedSize > maxBytes {
		return nil, fmt.Errorf("asset size %d bytes exceeds limit of %d bytes", expectedSize, maxBytes)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	attachGitHubToken(req, authScopeDownload)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.ContentLength > 0 && resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("download size %d bytes exceeds limit of %d bytes", resp.ContentLength, maxBytes)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "ghinst-*")
	if err != nil {
		return nil, err
	}

	written, err := io.Copy(tmp, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}

	if written > maxBytes {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, fmt.Errorf("download exceeded limit of %d bytes", maxBytes)
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}

	return tmp, nil
}

// installBinary places the binary under <baseDir>/ghinst/owner/repo@tag/
// and symlinks it into <baseDir>/bin/.
func installBinary(baseDir, owner, repo, tag, binName string, src *os.File) (_ string, err error) {
	installDir, _, err := managedInstallDir(baseDir, owner, repo, tag)
	if err != nil {
		return "", err
	}

	installDirPreExisted := true
	if _, statErr := os.Lstat(installDir); os.IsNotExist(statErr) {
		installDirPreExisted = false
	} else if statErr != nil {
		return "", statErr
	}

	if err := os.MkdirAll(installDir, 0755); err != nil {
		return "", err
	}

	if err := ensurePathNotSymlink(installDir); err != nil {
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

	linkDir, linkPath, err := managedLinkPath(baseDir, binName)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(linkDir, 0755); err != nil {
		return "", err
	}

	if err := ensurePathNotSymlink(linkDir); err != nil {
		return "", err
	}

	if info, err := os.Lstat(linkPath); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return "", fmt.Errorf("refusing to replace non-symlink %s", linkPath)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	tmpLink, err := os.CreateTemp(linkDir, "."+binName+".tmp-*")
	if err != nil {
		return "", err
	}
	tmpLinkPath := tmpLink.Name()
	if err := tmpLink.Close(); err != nil {
		os.Remove(tmpLinkPath)
		return "", err
	}
	if err := os.Remove(tmpLinkPath); err != nil {
		return "", err
	}
	defer os.Remove(tmpLinkPath)

	if err := os.Symlink(binPath, tmpLinkPath); err != nil {
		return "", err
	}

	if err := os.Rename(tmpLinkPath, linkPath); err != nil {
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
	binDir := managedBinDir(baseDir)
	if err := ensurePathNotSymlink(binDir); err != nil {
		return err
	}

	links, err := os.ReadDir(binDir)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else {
		for _, l := range links {
			linkPath := filepath.Join(binDir, l.Name())
			target, err := os.Readlink(linkPath)
			if err == nil {
				active[filepath.Dir(target)] = true
			}
		}
	}

	ghinstDir := managedGhinstRoot(baseDir)
	if err := ensurePathNotSymlink(ghinstDir); err != nil {
		return err
	}

	owners, err := os.ReadDir(ghinstDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	for _, owner := range owners {
		if owner.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to use symlinked path %s", filepath.Join(ghinstDir, owner.Name()))
		}

		if !owner.IsDir() {
			continue
		}

		ownerDir, err := managedOwnerDir(baseDir, owner.Name())
		if err != nil {
			return err
		}

		entries, err := os.ReadDir(ownerDir)
		if err != nil {
			return err
		}

		sort.Slice(entries, func(i, j int) bool {
			ri, vi, _ := strings.Cut(entries[i].Name(), "@")
			rj, vj, _ := strings.Cut(entries[j].Name(), "@")
			if ri != rj {
				return ri < rj
			}

			return vi > vj
		})
		for _, e := range entries {
			if e.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing to use symlinked path %s", filepath.Join(ownerDir, e.Name()))
			}

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

			fmt.Printf("%s %s/%s %s\n", marker, owner.Name(), repo, decodeTagFromPathComponent(version))
		}
	}

	return nil
}

// purge removes all but the currently linked version of owner/repo.
func purge(baseDir, owner, repo string) error {
	if err := validateTargetParts(owner, repo); err != nil {
		return err
	}

	ownerDir, err := managedOwnerDir(baseDir, owner)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	entries, err := os.ReadDir(ownerDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	var versions []os.DirEntry
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to use symlinked path %s", filepath.Join(ownerDir, e.Name()))
		}

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
	binDir := managedBinDir(baseDir)
	if err := ensurePathNotSymlink(binDir); err != nil && !os.IsNotExist(err) {
		return err
	}

	if links, err := os.ReadDir(binDir); err == nil {
		for _, l := range links {
			target, err := os.Readlink(filepath.Join(binDir, l.Name()))
			if err != nil {
				continue
			}

			dir := filepath.Dir(target)
			base := filepath.Base(dir)
			if filepath.Dir(dir) == ownerDir && strings.HasPrefix(base, repo+"@") {
				active = base
				break
			}
		}
	}

	if active == "" {
		return fmt.Errorf("could not determine active version for %s/%s", owner, repo)
	}

	for _, v := range versions {
		if v.Name() == active {
			continue
		}

		dir, err := managedJoin(ownerDir, v.Name())
		if err != nil {
			return err
		}

		if err := ensurePathNotSymlink(dir); err != nil {
			return err
		}

		if err := os.RemoveAll(dir); err != nil {
			return err
		}

		fmt.Printf("purged %s/%s\n", owner, v.Name())
	}

	return nil
}
