package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sakurai-ryo/innolens/internal/tui"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: innolens <datadir> [<baseline datadir>]")
		os.Exit(2)
	}
	base := ""
	if len(os.Args) == 3 {
		base = os.Args[2]
		// A baseline that cannot be read shows no diff, which looks the same
		// as a page the statement did not touch.
		if fi, err := os.Stat(base); err != nil || !fi.IsDir() {
			fmt.Fprintf(os.Stderr, "baseline %s is not a directory\n", base)
			os.Exit(1)
		}
	}
	m, err := tui.New(os.Args[1], base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
