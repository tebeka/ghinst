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
}

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
	flag.BoolVar(&options.showVersion, "version", false, "print version and exit")
	flag.BoolVar(&options.doPurge, "purge", false, "remove all but the latest installed version of owner/repo")
	flag.BoolVar(&options.list, "list", false, "list installed apps")
	flag.BoolVar(&options.force, "force", false, "install even if already on the latest version")
	flag.StringVar(&options.baseDir, "dir", defaultBaseDir(), "base install directory (overrides GHINST_DIR)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s owner/repo[@version]\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()

	if options.showVersion {
		fmt.Printf("%s %s\n", filepath.Base(os.Args[0]), buildVersion())
		return
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

	if options.baseDir == "" {
		fmt.Fprintln(os.Stderr, "error: could not determine install base dir; set -dir or GHINST_DIR")
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

	installDir := filepath.Join(options.baseDir, "ghinst", owner, repo+"@"+release.TagName)
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
