package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const encodedTagPrefix = "~"

var githubSlugRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validateTargetParts(owner, repo string) error {
	if err := validateGitHubSlugComponent("owner", owner); err != nil {
		return err
	}

	return validateGitHubSlugComponent("repo", repo)
}

func validateGitHubSlugComponent(kind, value string) error {
	if value == "." || value == ".." || !githubSlugRE.MatchString(value) {
		return fmt.Errorf("invalid %s %q", kind, value)
	}

	return nil
}

func validatePathComponent(kind, value string) error {
	if value == "" || value == "." || value == ".." {
		return fmt.Errorf("invalid %s %q", kind, value)
	}

	if strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("invalid %s %q", kind, value)
	}

	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return fmt.Errorf("invalid %s %q", kind, value)
		}
	}

	return nil
}

func encodeTagForPath(tag string) string {
	return encodedTagPrefix + url.PathEscape(tag)
}

func decodeTagFromPathComponent(tag string) string {
	if !strings.HasPrefix(tag, encodedTagPrefix) {
		return tag
	}

	decoded, err := url.PathUnescape(strings.TrimPrefix(tag, encodedTagPrefix))
	if err != nil {
		return tag
	}

	return decoded
}

func managedJoin(root string, elems ...string) (string, error) {
	path := filepath.Join(append([]string{root}, elems...)...)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path escapes managed root: %s", path)
	}

	return path, nil
}

func ensurePathNotSymlink(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to use symlinked path %s", path)
	}

	return nil
}

func managedGhinstRoot(baseDir string) string {
	return filepath.Join(baseDir, "ghinst")
}

func managedBinDir(baseDir string) string {
	return filepath.Join(baseDir, "bin")
}

func managedOwnerDir(baseDir, owner string) (string, error) {
	if err := validateGitHubSlugComponent("owner", owner); err != nil {
		return "", err
	}

	root := managedGhinstRoot(baseDir)
	if err := ensurePathNotSymlink(root); err != nil {
		return "", err
	}

	path, err := managedJoin(root, owner)
	if err != nil {
		return "", err
	}

	if err := ensurePathNotSymlink(path); err != nil {
		return "", err
	}

	return path, nil
}

func managedInstallDir(baseDir, owner, repo, tag string) (string, string, error) {
	if err := validateTargetParts(owner, repo); err != nil {
		return "", "", err
	}

	ownerDir, err := managedOwnerDir(baseDir, owner)
	if err != nil {
		return "", "", err
	}

	dirName := repo + "@" + encodeTagForPath(tag)
	if err := validatePathComponent("install directory", dirName); err != nil {
		return "", "", err
	}

	path, err := managedJoin(ownerDir, dirName)
	if err != nil {
		return "", "", err
	}

	if err := ensurePathNotSymlink(path); err != nil {
		return "", "", err
	}

	return path, dirName, nil
}

func managedLinkPath(baseDir, binName string) (string, string, error) {
	if err := validatePathComponent("binary name", binName); err != nil {
		return "", "", err
	}

	linkDir := managedBinDir(baseDir)
	if err := ensurePathNotSymlink(linkDir); err != nil {
		return "", "", err
	}

	linkPath, err := managedJoin(linkDir, binName)
	if err != nil {
		return "", "", err
	}

	return linkDir, linkPath, nil
}
