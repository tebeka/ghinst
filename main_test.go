package main

import (
	"flag"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMainListRequiresBaseDir(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestMainListRequiresBaseDirHelper", "--", "-list", "-dir", "")
	cmd.Env = append(os.Environ(), "GHINST_TEST_MAIN=1")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit status")
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T (%v)", err, err)
	}

	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\noutput:\n%s", exitErr.ExitCode(), out)
	}

	if !strings.Contains(string(out), "could not determine install base dir") {
		t.Fatalf("expected base-dir validation error, got output:\n%s", out)
	}
}

func TestMainRejectsNonPositiveMaxSize(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestMainListRequiresBaseDirHelper", "--", "-list", "-max-size", "0")
	cmd.Env = append(os.Environ(), "GHINST_TEST_MAIN=1")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit status")
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T (%v)", err, err)
	}

	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\noutput:\n%s", exitErr.ExitCode(), out)
	}

	if !strings.Contains(string(out), "-max-size must be greater than 0") {
		t.Fatalf("expected max-size validation error, got output:\n%s", out)
	}
}

func TestMainRejectsNonPositiveHTTPTimeout(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestMainListRequiresBaseDirHelper", "--", "-list", "-http-timeout", "0s")
	cmd.Env = append(os.Environ(), "GHINST_TEST_MAIN=1")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit status")
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T (%v)", err, err)
	}

	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\noutput:\n%s", exitErr.ExitCode(), out)
	}

	if !strings.Contains(string(out), "-http-timeout must be greater than 0") {
		t.Fatalf("expected http-timeout validation error, got output:\n%s", out)
	}
}

func TestMainListRequiresBaseDirHelper(t *testing.T) {
	if os.Getenv("GHINST_TEST_MAIN") != "1" {
		return
	}

	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			break
		}
	}

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	main()
}

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		value string
		want  int64
	}{
		{value: "1", want: 1},
		{value: "2mb", want: 2 * mib},
		{value: "2MB", want: 2 * mib},
		{value: "512kb", want: 512 * 1024},
		{value: "3gib", want: 3 * (1 << 30)},
		{value: "4096b", want: 4096},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := parseByteSize(tt.value)
			if err != nil {
				t.Fatalf("parseByteSize(%q) error: %v", tt.value, err)
			}

			if got != tt.want {
				t.Fatalf("parseByteSize(%q) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseByteSizeRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "mb", "1tb", "nope", "1.5mb"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			if _, err := parseByteSize(value); err == nil {
				t.Fatalf("parseByteSize(%q) expected error", value)
			}
		})
	}
}

func TestValidateOptionsUpdatesHTTPClientTimeout(t *testing.T) {
	oldOptions := options
	oldTimeout := httpClient.Timeout
	t.Cleanup(func() {
		options = oldOptions
		httpClient.Timeout = oldTimeout
	})

	options.baseDir = t.TempDir()
	options.maxSize = 1
	options.httpTimeout = 45 * time.Second

	if err := validateOptions(); err != nil {
		t.Fatalf("validateOptions: %v", err)
	}

	if httpClient.Timeout != options.httpTimeout {
		t.Fatalf("httpClient.Timeout = %v, want %v", httpClient.Timeout, options.httpTimeout)
	}
}

func TestExtractedBinarySizeLimitMatchesConfiguredMaxSize(t *testing.T) {
	for _, maxSize := range []int64{5 * mib, 200 * mib} {
		if got := extractedBinarySizeLimit(maxSize); got != maxSize {
			t.Fatalf("extractedBinarySizeLimit(%d) = %d, want %d", maxSize, got, maxSize)
		}
	}
}
