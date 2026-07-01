package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	"github.com/davidliu/lrpush/internal/locate"
)

// pickIndex shows an arrow-key menu (huh) on a TTY, or a numbered stdin prompt
// otherwise, and returns the chosen 0-based index.
func pickIndex(title string, labels []string) (int, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		var sel int
		opts := make([]huh.Option[int], len(labels))
		for i, l := range labels {
			opts[i] = huh.NewOption(l, i)
		}
		err := huh.NewSelect[int]().Title(title).Options(opts...).Value(&sel).Run()
		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return -1, fmt.Errorf("selection cancelled")
			}
			return -1, err
		}
		return sel, nil
	}
	// non-TTY fallback
	fmt.Println(title)
	for i, l := range labels {
		fmt.Printf("  [%d] %s\n", i+1, l)
	}
	fmt.Print("Enter number: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return -1, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(labels) {
		return -1, fmt.Errorf("invalid selection %q", strings.TrimSpace(line))
	}
	return n - 1, nil
}

// catalogPicker adapts pickIndex to locate.SelectCatalog's picker signature.
func catalogPicker(cands []locate.Catalog) (int, error) {
	labels := make([]string, len(cands))
	for i, c := range cands {
		labels[i] = fmt.Sprintf("%s (%d presets)", c.Name, c.PresetCount)
	}
	return pickIndex("Select a catalog", labels)
}
