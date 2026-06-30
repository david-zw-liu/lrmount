package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/davidliu/lrpush/internal/afcfs"
	"github.com/davidliu/lrpush/internal/device"
	"github.com/davidliu/lrpush/internal/locate"
	"github.com/davidliu/lrpush/internal/rmsync"
)

func newRmCmd() *cobra.Command {
	var (
		backupDir   string
		commit      bool
		catalog     string
		interactive bool
	)
	cmd := &cobra.Command{
		Use:   "rm [path...]",
		Short: "Delete files/folders from the device userStyles (default dry-run)",
		Long: "Delete files/folders from the device userStyles (default dry-run).\n\n" +
			"Pass relative paths as arguments, or use --interactive/-i to pick from a\n" +
			"multi-select menu of userStyles' first-level entries.",
		Args: func(cmd *cobra.Command, args []string) error {
			if interactive {
				return nil // targets come from the menu, not positional args
			}
			return cobra.MinimumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Banner: interactive confirms before deleting (so always relevant);
			// non-interactive only mutates with --commit.
			if commit || interactive {
				fmt.Print(warningBanner())
			}
			sess, err := device.Connect(flagUDID, bundleCandidates(cmd)...)
			if err != nil {
				return err
			}
			defer sess.Close()
			fmt.Printf("device: %s   bundle: %s\n", sess.Label, sess.BundleID)

			docsRoot, err := locate.DocumentsRoot(sess.FS, flagPathPrefix)
			if err != nil {
				return err
			}
			cands, err := locate.FindCatalogs(sess.FS, docsRoot)
			if err != nil {
				return err
			}
			chosen, err := locate.SelectCatalog(cands, catalog, terminalPicker)
			if err != nil {
				return err
			}
			fmt.Printf("target userStyles: %s\n", chosen.UserStyles)

			if backupDir == "" {
				backupDir = filepath.Join("./_userStyles_backup", time.Now().Format("20060102-150405"))
			}

			// Interactive mode is self-contained: pick → confirm → backup + delete.
			if interactive {
				rels, ok, err := interactiveSelectTargets(sess.FS, chosen.UserStyles, os.Stdout)
				if err != nil {
					return err
				}
				if !ok {
					return nil // user cancelled or selected nothing
				}
				targets := rmsync.PlanRm(sess.FS, chosen.UserStyles, rels)
				return rmsync.Execute(sess.FS, targets, rmsync.ExecOptions{
					BackupDir: backupDir,
					Commit:    true,
					Out:       os.Stdout,
				})
			}

			targets := rmsync.PlanRm(sess.FS, chosen.UserStyles, args)
			return rmsync.Execute(sess.FS, targets, rmsync.ExecOptions{
				BackupDir: backupDir,
				Commit:    commit,
				Out:       os.Stdout,
			})
		},
	}
	cmd.Flags().StringVar(&backupDir, "backup-dir", "", "backup dir (default ./_userStyles_backup/<timestamp>)")
	cmd.Flags().BoolVar(&commit, "commit", false, "actually delete on device (otherwise dry-run)")
	cmd.Flags().StringVar(&catalog, "catalog", "", "select catalog by name (non-interactive)")
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "pick targets from a multi-select menu of userStyles entries, then confirm and delete")
	return cmd
}

// interactiveSelectTargets shows a multi-select menu of userStyles' first-level
// entries, then asks for confirmation. It returns the chosen relative paths and
// ok=true only when the user confirms a non-empty selection. On a real terminal
// it uses an arrow-key TUI; otherwise it falls back to a numbered prompt.
func interactiveSelectTargets(fs afcfs.FS, userStyles string, w io.Writer) ([]string, bool, error) {
	entries, err := listUserStylesEntries(fs, userStyles)
	if err != nil {
		return nil, false, err
	}
	if len(entries) == 0 {
		fmt.Fprintln(w, "userStyles is empty; nothing to delete")
		return nil, false, nil
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return tuiMultiSelect(entries)
	}
	return numberSelectTargets(entries, w)
}

// numberSelectTargets is the non-TTY fallback: print a numbered list, read a
// selection line, then a typed "yes" confirmation.
func numberSelectTargets(entries []entryChoice, w io.Writer) ([]string, bool, error) {
	fmt.Fprintln(w, "userStyles entries:")
	for i, e := range entries {
		kind := "file"
		if e.IsDir {
			kind = "dir "
		}
		fmt.Fprintf(w, "  [%d] %s %s\n", i+1, kind, e.Name)
	}
	fmt.Fprint(w, "Select items to delete (e.g. '1 3 5' or 'all'; empty to cancel): ")
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		return nil, false, err
	}
	sel, err := parseSelection(line, len(entries))
	if err != nil {
		return nil, false, err
	}
	if len(sel) == 0 {
		fmt.Fprintln(w, "nothing selected; aborting")
		return nil, false, nil
	}
	var rels []string
	fmt.Fprintln(w, "Will delete:")
	for _, i := range sel {
		rels = append(rels, entries[i].Name)
		fmt.Fprintf(w, "  - %s\n", entries[i].Name)
	}
	fmt.Fprint(w, "Confirm delete these item(s)? Type 'yes' to proceed: ")
	confirm, err := stdinReader.ReadString('\n')
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(confirm) != "yes" {
		fmt.Fprintln(w, "aborted")
		return nil, false, nil
	}
	return rels, true, nil
}
