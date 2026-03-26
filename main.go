package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

var options struct {
	showVersion bool
	purge       bool
	list        bool
	force       bool
	baseDir     string
	completion  string
	maxSize     byteSize
	httpTimeout time.Duration
}

const (
	defaultMaxAssetSizeMiB       int64 = 200
	defaultMaxExtractedSizeBytes int64 = 100 << 20
	mib                          int64 = 1 << 20
)

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	v := strings.TrimSuffix(info.Main.Version, "+dirty")
	// Pseudo-versions embed a git hash (v0.0.0-YYYYMMDDHHMMSS-abcdef123456);
	// strip everything after the timestamp.
	if parts := strings.SplitN(v, "-", 3); len(parts) == 3 {
		v = parts[0] + "-" + parts[1]
	}

	return v
}

func registerFlags(fs *flag.FlagSet) {
	fs.StringVar(&options.completion, "completion", "", "print shell completion script (bash, zsh, fish)")
	fs.BoolVar(&options.showVersion, "version", false, "print version and exit")
	fs.BoolVar(&options.purge, "purge", false, "remove all but the currently used version of owner/repo")
	fs.BoolVar(&options.list, "list", false, "list installed apps")
	fs.BoolVar(&options.force, "force", false, "install even if already on the latest version")
	fs.StringVar(&options.baseDir, "dir", defaultBaseDir(), "base install directory (overrides GHINST_DIR)")
	options.maxSize = byteSize(defaultMaxAssetSizeMiB * mib)
	fs.Var(&options.maxSize, "max-size", "maximum downloaded asset size in bytes (supports kb, mb, gb suffixes)")
	fs.DurationVar(&options.httpTimeout, "http-timeout", httpClient.Timeout, "HTTP timeout (supports time.ParseDuration formats)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s owner/repo[@version]\n", filepath.Base(os.Args[0]))
		fs.PrintDefaults()
	}
}

func main() {
	registerFlags(flag.CommandLine)
	flag.Parse()

	if options.completion != "" {
		if err := printCompletion(options.completion); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		return
	}

	if options.showVersion {
		fmt.Printf("%s %s\n", filepath.Base(os.Args[0]), buildVersion())
		return
	}

	if err := validateOptions(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if options.list {
		if err := listInstalled(options.baseDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		return
	}

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "error: wrong number of arguments")
		os.Exit(1)
	}

	pkg := flag.Arg(0)

	owner, repo, tag, err := parseTarget(pkg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if options.purge {
		err = purge(options.baseDir, owner, repo)
	} else {
		err = handleInstall(owner, repo, tag)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func validateOptions() error {
	if options.baseDir == "" {
		return fmt.Errorf("could not determine install base dir; set -dir or GHINST_DIR")
	}

	if options.maxSize <= 0 {
		return fmt.Errorf("-max-size must be greater than 0")
	}

	if options.httpTimeout <= 0 {
		return fmt.Errorf("-http-timeout must be greater than 0")
	}

	httpClient.Timeout = options.httpTimeout

	return nil
}

func handleInstall(owner, repo, tag string) error {
	release, err := fetchRelease(owner, repo, tag)
	if err != nil {
		return err
	}

	installNeeded, err := ensureInstallNeeded(owner, repo, release.TagName)
	if err != nil {
		return err
	}

	if !installNeeded {
		return nil
	}

	asset, err := selectAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		printAvailableAssets(release.Assets)
		return err
	}

	linkPath, err := installReleaseAsset(owner, repo, release.TagName, asset)
	if err != nil {
		return err
	}

	fmt.Printf("installed %s (%s) → %s\n", repo, release.TagName, linkPath)
	return nil
}

func ensureInstallNeeded(owner, repo, tag string) (bool, error) {
	if options.force {
		return true, nil
	}

	installDir, _, err := managedInstallDir(options.baseDir, owner, repo, tag)
	if err != nil {
		return false, fmt.Errorf("resolving install directory: %w", err)
	}

	healthy, err := isHealthyInstallDir(installDir)
	if err != nil {
		return false, fmt.Errorf("checking existing install: %w", err)
	}

	if healthy {
		fmt.Printf("%s/%s is already at %s\n", owner, repo, tag)
		return false, nil
	}

	return true, nil
}

func installReleaseAsset(owner, repo, tag string, asset Asset) (string, error) {
	maxAssetSize := int64(options.maxSize)
	tmp, err := downloadAndVerify(asset, maxAssetSize)
	if err != nil {
		return "", err
	}

	defer os.Remove(tmp.Name())
	defer tmp.Close()

	maxExtractedSize := min(defaultMaxExtractedSizeBytes, maxAssetSize)
	binName, binFile, err := extractBinary(tmp, asset.Name, maxExtractedSize)
	if err != nil {
		return "", fmt.Errorf("extracting: %w", err)
	}

	defer os.Remove(binFile.Name())
	defer binFile.Close()

	linkPath, err := installBinary(options.baseDir, owner, repo, tag, binName, binFile)
	if err != nil {
		return "", fmt.Errorf("installing: %w", err)
	}

	return linkPath, nil
}

type byteSize int64

func (s *byteSize) Set(value string) error {
	size, err := parseByteSize(value)
	if err != nil {
		return err
	}

	*s = byteSize(size)
	return nil
}

func (s *byteSize) String() string {
	if s == nil || *s == 0 {
		return "0"
	}

	size := int64(*s)
	if size%mib == 0 {
		return strconv.FormatInt(size/mib, 10) + "mb"
	}

	return strconv.FormatInt(size, 10) + "b"
}

func parseByteSize(value string) (int64, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, fmt.Errorf("invalid size %q", value)
	}

	for _, unit := range []struct {
		suffix string
		scale  int64
	}{
		{"kib", 1 << 10},
		{"kb", 1 << 10},
		{"mib", mib},
		{"mb", mib},
		{"gib", 1 << 30},
		{"gb", 1 << 30},
		{"b", 1},
	} {
		if !strings.HasSuffix(strings.ToLower(raw), unit.suffix) {
			continue
		}

		n, err := strconv.ParseInt(strings.TrimSpace(raw[:len(raw)-len(unit.suffix)]), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid size %q", value)
		}

		return n * unit.scale, nil
	}

	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", value)
	}

	return n, nil
}

func downloadAndVerify(asset Asset, maxAssetSize int64) (*os.File, error) {
	tmp, err := download(asset.BrowserDownloadURL, asset.Size, maxAssetSize)
	if err != nil {
		return nil, fmt.Errorf("downloading: %w", err)
	}

	if err := verifyAssetDigest(asset, tmp, os.Stderr); err != nil {
		os.Remove(tmp.Name())
		tmp.Close()
		return nil, fmt.Errorf("verifying checksum: %w", err)
	}

	return tmp, nil
}

func printAvailableAssets(assets []Asset) {
	fmt.Fprintln(os.Stderr, "available assets:")
	for _, a := range assets {
		fmt.Fprintf(os.Stderr, "  %s\n", a.Name)
	}
}

func isHealthyInstallDir(installDir string) (bool, error) {
	info, err := os.Stat(installDir)
	if os.IsNotExist(err) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	if !info.IsDir() {
		return false, fmt.Errorf("install path is not a directory: %s", installDir)
	}

	entries, err := os.ReadDir(installDir)
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fi, err := entry.Info()
		if err != nil {
			continue
		}

		if fi.Mode().IsRegular() && fi.Mode()&0111 != 0 {
			return true, nil
		}
	}

	return false, nil
}
