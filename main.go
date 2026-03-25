package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
)

var options struct {
	showVersion bool
	doPurge     bool
	list        bool
	force       bool
	baseDir     string
	completion  string
	maxSizeMiB  int64
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

func main() {
	flag.StringVar(&options.completion, "completion", "", "print shell completion script (bash, zsh, fish)")
	flag.BoolVar(&options.showVersion, "version", false, "print version and exit")
	flag.BoolVar(&options.doPurge, "purge", false, "remove all but the currently used version of owner/repo")
	flag.BoolVar(&options.list, "list", false, "list installed apps")
	flag.BoolVar(&options.force, "force", false, "install even if already on the latest version")
	flag.StringVar(&options.baseDir, "dir", defaultBaseDir(), "base install directory (overrides GHINST_DIR)")
	flag.Int64Var(&options.maxSizeMiB, "max-size", defaultMaxAssetSizeMiB, "maximum downloaded asset size in MiB")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s owner/repo[@version]\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}

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

	if options.baseDir == "" {
		fmt.Fprintln(os.Stderr, "error: could not determine install base dir; set -dir or GHINST_DIR")
		os.Exit(1)
	}

	if options.maxSizeMiB <= 0 {
		fmt.Fprintln(os.Stderr, "error: -max-size must be greater than 0")
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
		flag.Usage()
		os.Exit(1)
	}

	owner, repo, tag, err := parseTarget(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if options.doPurge {
		if err := purge(options.baseDir, owner, repo); err != nil {
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

	installDir, _, err := managedInstallDir(options.baseDir, owner, repo, release.TagName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: resolving install directory: %v\n", err)
		os.Exit(1)
	}

	if !options.force {
		healthy, err := isHealthyInstallDir(installDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: checking existing install: %v\n", err)
			os.Exit(1)
		}

		if healthy {
			fmt.Printf("%s/%s is already at %s\n", owner, repo, release.TagName)
			return
		}
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

	maxAssetSize := options.maxSizeMiB * mib
	tmp, err := download(asset.BrowserDownloadURL, asset.Size, maxAssetSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: downloading: %v\n", err)
		os.Exit(1)
	}

	defer os.Remove(tmp.Name())
	defer tmp.Close()

	maxExtractedSize := defaultMaxExtractedSizeBytes
	if maxAssetSize < maxExtractedSize {
		maxExtractedSize = maxAssetSize
	}

	binName, binFile, err := extractBinary(tmp, asset.Name, maxExtractedSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: extracting: %v\n", err)
		os.Exit(1)
	}

	defer os.Remove(binFile.Name())
	defer binFile.Close()

	linkPath, err := installBinary(options.baseDir, owner, repo, release.TagName, binName, binFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: installing: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("installed %s (%s) → %s\n", repo, release.TagName, linkPath)
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
