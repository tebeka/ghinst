package main

import (
	"embed"
	"fmt"
)

//go:embed _comp
var compFS embed.FS

var compFiles = map[string]string{
	"bash": "_comp/ghinst.bash",
	"zsh":  "_comp/ghinst.zsh",
	"fish": "_comp/ghinst.fish",
}

func printCompletion(shell string) error {
	path, ok := compFiles[shell]
	if !ok {
		return fmt.Errorf("unknown shell %q; supported: bash, zsh, fish", shell)
	}
	data, err := compFS.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}
