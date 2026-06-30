package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/davidliu/lrpush/internal/device"
	"github.com/davidliu/lrpush/internal/locate"
	"github.com/davidliu/lrpush/internal/pushsync"
)

func newPushCmd() *cobra.Command {
	var (
		source    string
		backupDir string
		commit    bool
		catalog   string
	)
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push local presets into the device userStyles (default dry-run)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if source == "" {
				return fmt.Errorf("--source is required")
			}
			if commit {
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

			plan, err := pushsync.PlanPush(source, chosen.UserStyles)
			if err != nil {
				return err
			}
			if backupDir == "" {
				backupDir = filepath.Join("./_userStyles_backup", time.Now().Format("20060102-150405"))
			}
			return pushsync.Execute(sess.FS, plan, pushsync.ExecOptions{
				UserStylesDir: chosen.UserStyles,
				BackupDir:     backupDir,
				Commit:        commit,
				Out:           os.Stdout,
			})
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "local file or folder to push (required)")
	cmd.Flags().StringVar(&backupDir, "backup-dir", "", "backup dir (default ./_userStyles_backup/<timestamp>)")
	cmd.Flags().BoolVar(&commit, "commit", false, "actually write to device (otherwise dry-run)")
	cmd.Flags().StringVar(&catalog, "catalog", "", "select catalog by name (non-interactive)")
	return cmd
}
