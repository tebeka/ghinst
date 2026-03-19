package main

import (
	"bytes"
	"os/exec"
	"testing"
)

func TestCompletionScripts(t *testing.T) {
	shells := []struct {
		name string
		args []string
	}{
		{"bash", []string{"-n"}},
		{"zsh", []string{"-n"}},
		{"fish", []string{"--no-execute"}},
	}

	for _, sh := range shells {
		t.Run(sh.name, func(t *testing.T) {
			shellPath, err := exec.LookPath(sh.name)
			if err != nil {
				t.Skipf("%s not found", sh.name)
			}

			script, err := compFS.ReadFile(compFiles[sh.name])
			if err != nil {
				t.Fatalf("read completion script: %v", err)
			}

			cmd := exec.Command(shellPath, sh.args...)
			cmd.Stdin = bytes.NewReader(script)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s completion script has errors: %v\n%s", sh.name, err, out)
			}
		})
	}
}
