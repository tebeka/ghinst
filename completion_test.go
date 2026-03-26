package main

import (
	"bytes"
	"flag"
	"os/exec"
	"regexp"
	"slices"
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

func TestCompletionScriptsMatchCurrentFlags(t *testing.T) {
	fs := flag.NewFlagSet("ghinst", flag.ContinueOnError)
	registerFlags(fs)

	wantFlags := make([]string, 0)
	fs.VisitAll(func(f *flag.Flag) {
		wantFlags = append(wantFlags, "-"+f.Name)
	})
	slices.Sort(wantFlags)

	parsers := map[string]func([]byte) []string{
		"bash": parseBashCompletionFlags,
		"zsh":  parseZshCompletionFlags,
		"fish": parseFishCompletionFlags,
	}

	for shell, parse := range parsers {
		t.Run(shell, func(t *testing.T) {
			script, err := compFS.ReadFile(compFiles[shell])
			if err != nil {
				t.Fatalf("read completion script: %v", err)
			}

			gotFlags := parse(script)
			slices.Sort(gotFlags)

			if !slices.Equal(gotFlags, wantFlags) {
				t.Fatalf("%s flags = %v, want %v", shell, gotFlags, wantFlags)
			}
		})
	}
}

func parseBashCompletionFlags(script []byte) []string {
	re := regexp.MustCompile(`compgen -W "([^"]+)"`)
	matches := re.FindAllSubmatch(script, -1)
	flags := make([]string, 0)
	for _, match := range matches {
		fields := bytes.FieldsSeq(match[1])
		for field := range fields {
			if len(field) > 0 && field[0] == '-' {
				flags = append(flags, string(field))
			}
		}
	}

	return flags
}

func parseZshCompletionFlags(script []byte) []string {
	re := regexp.MustCompile(`'(-[^[]+)\[`)
	matches := re.FindAllSubmatch(script, -1)
	flags := make([]string, 0, len(matches))
	for _, match := range matches {
		flags = append(flags, string(match[1]))
	}

	return flags
}

func parseFishCompletionFlags(script []byte) []string {
	re := regexp.MustCompile(`-o ([A-Za-z0-9-]+)`)
	matches := re.FindAllSubmatch(script, -1)
	flags := make([]string, 0, len(matches))
	for _, match := range matches {
		flags = append(flags, "-"+string(match[1]))
	}

	return flags
}
