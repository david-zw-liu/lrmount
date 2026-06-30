package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/davidliu/lrpush/internal/device"
	"github.com/davidliu/lrpush/internal/inspect"
	"github.com/davidliu/lrpush/internal/locate"
)

// stdinReader is shared so successive prompts don't lose bytes to a fresh
// bufio.Reader's read-ahead buffer (terminalPicker is reused by push/rm).
var stdinReader = bufio.NewReader(os.Stdin)

// terminalPicker prints a numbered menu and reads a choice from stdin.
func terminalPicker(cands []locate.Catalog) (int, error) {
	fmt.Println("Multiple catalogs found:")
	for i, c := range cands {
		fmt.Printf("  [%d] %s (userStyles files: %d)\n", i, c.Name, c.PresetCount)
	}
	fmt.Print("Select catalog number: ")
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		return -1, err
	}
	return strconv.Atoi(strings.TrimSpace(line))
}

func newInspectCmd() *cobra.Command {
	var catalog string
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Locate userStyles and list its first-level contents",
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := device.Connect(flagUDID, bundleCandidates(cmd)...)
			if err != nil {
				return err
			}
			defer sess.Close()
			fmt.Printf("device: %s   bundle: %s\n\n", sess.Label, sess.BundleID)
			return inspect.Run(sess.FS, os.Stdout, inspect.Options{
				PathPrefix:  flagPathPrefix,
				CatalogFlag: catalog,
				Picker:      terminalPicker,
			})
		},
	}
	cmd.Flags().StringVar(&catalog, "catalog", "", "select catalog by name (non-interactive)")
	return cmd
}
