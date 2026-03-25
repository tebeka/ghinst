package main

import (
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
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
