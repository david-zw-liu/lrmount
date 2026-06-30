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

// terminalPicker prints a numbered menu and reads a choice from stdin.
func terminalPicker(cands []locate.Catalog) (int, error) {
	fmt.Println("Multiple catalogs found:")
	for i, c := range cands {
		fmt.Printf("  [%d] %s (userStyles files: %d)\n", i, c.Name, c.PresetCount)
	}
	fmt.Print("Select catalog number: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return -1, err
	}
	return strconv.Atoi(strings.TrimSpace(line))
}

func newInspectCmd() *cobra.Command {
	var samples int
	var sampleDir string
	var catalog string
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Dump the app container tree and locate userStyles",
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := device.Connect(flagUDID, flagBundleID)
			if err != nil {
				return err
			}
			defer sess.Close()
			fmt.Printf("device: %s   bundle: %s\n\n", sess.Label, flagBundleID)
			return inspect.Run(sess.FS, os.Stdout, inspect.Options{
				PathPrefix:  flagPathPrefix,
				Samples:     samples,
				SampleDir:   sampleDir,
				CatalogFlag: catalog,
				Picker:      terminalPicker,
			})
		},
	}
	cmd.Flags().IntVar(&samples, "sample", 1, "how many existing userStyles files to pull for format inspection")
	cmd.Flags().StringVar(&sampleDir, "sample-dir", "./_inspect_sample", "local dir for pulled samples")
	cmd.Flags().StringVar(&catalog, "catalog", "", "select catalog by name (non-interactive)")
	return cmd
}
