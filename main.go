package main

import (
	"os"

	"github.com/rtwsvj/hukou/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(cmd.ExitCode(err))
	}
}
