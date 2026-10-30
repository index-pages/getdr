// Command getdr checks Ahrefs Domain Rating for domains.
// See https://index-pages.com/getdr/
package main

import (
	"os"
	"time"

	"golang.org/x/term"

	"github.com/index-pages/getdr/internal/cli"
)

func main() {
	tty := func(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }
	env := &cli.Env{
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Getenv:    os.Getenv,
		StdinTTY:  tty(os.Stdin),
		StdoutTTY: tty(os.Stdout),
		StderrTTY: tty(os.Stderr),
		Now:       time.Now,
	}
	if env.StdoutTTY {
		if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			env.Width = w
		}
	}
	os.Exit(cli.Run(os.Args[1:], env))
}
