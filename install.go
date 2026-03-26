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

	resp, err := getGitHub(http.MethodGet, url, authScopeDownload)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.ContentLength > 0 && resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("download size %d bytes exceeds limit of %d bytes", resp.ContentLength, maxBytes)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	return copyToTempFile("", "ghinst-*", resp.Body, maxBytes)
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
	tmp, err := copyToTempFile(installDir, ".tmp-*", src, 0)
	if err != nil {
		return "", err
	}

	tmpName := tmp.Name()

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
	active, err := activeInstallDirs(baseDir)
	if err != nil {
		return err
	}

	ghinstDir := managedGhinstRoot(baseDir)
	if err := ensurePathNotSymlink(ghinstDir); err != nil {
		return err
	}

	owners, err := readDirIfExists(ghinstDir)
	if err != nil {
		return err
	}

	if owners == nil {
		return nil
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

			repo, encodedTag, ok := installDirParts(e.Name())
			if !ok {
				continue
			}

			marker := " "
			if active[filepath.Join(ownerDir, e.Name())] {
				marker = "*"
			}

			fmt.Printf("%s %s/%s %s\n", marker, owner.Name(), repo, decodeTagFromPathComponent(encodedTag))
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

	entries, err := readDirIfExists(ownerDir)
	if err != nil {
		return err
	}

	if entries == nil {
		return nil
	}

	versions, err := repoVersions(ownerDir, repo, entries)
	if err != nil {
		return err
	}

	if len(versions) <= 1 {
		return nil
	}

	active, err := activeInstallDirs(baseDir)
	if err != nil {
		return err
	}

	activeVersion := ""
	for _, v := range versions {
		if active[filepath.Join(ownerDir, v.Name())] {
			activeVersion = v.Name()
			break
		}
	}

	if activeVersion == "" {
		return fmt.Errorf("could not determine active version for %s/%s", owner, repo)
	}

	for _, v := range versions {
		if v.Name() == activeVersion {
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

func copyToTempFile(dir, pattern string, r io.Reader, maxBytes int64) (*os.File, error) {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}

	var written int64
	if maxBytes > 0 {
		written, err = io.Copy(tmp, io.LimitReader(r, maxBytes+1))
		if err == nil && written > maxBytes {
			err = fmt.Errorf("size exceeds limit of %d bytes", maxBytes)
		}
	} else {
		_, err = io.Copy(tmp, r)
	}

	if err != nil {
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

func readDirIfExists(path string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil, nil
	}

	return entries, err
}

func activeInstallDirs(baseDir string) (map[string]bool, error) {
	active := map[string]bool{}
	binDir := managedBinDir(baseDir)
	if err := ensurePathNotSymlink(binDir); err != nil {
		return nil, err
	}

	links, err := readDirIfExists(binDir)
	if err != nil {
		return nil, err
	}

	for _, l := range links {
		target, err := os.Readlink(filepath.Join(binDir, l.Name()))
		if err == nil {
			active[filepath.Dir(target)] = true
		}
	}

	return active, nil
}

func repoVersions(ownerDir, repo string, entries []os.DirEntry) ([]os.DirEntry, error) {
	var versions []os.DirEntry
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing to use symlinked path %s", filepath.Join(ownerDir, e.Name()))
		}

		if !e.IsDir() {
			continue
		}

		name, _, ok := installDirParts(e.Name())
		if ok && name == repo {
			versions = append(versions, e)
		}
	}

	return versions, nil
}

func installDirParts(name string) (repo, encodedTag string, ok bool) {
	repo, encodedTag, ok = strings.Cut(name, "@")
	if !ok || repo == "" || encodedTag == "" {
		return "", "", false
	}

	return repo, encodedTag, true
}
