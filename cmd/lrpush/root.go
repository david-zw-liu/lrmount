package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/davidliu/lrpush/internal/device"
	"github.com/davidliu/lrpush/internal/locate"
)

// lightroomBundleIDs are probed in order; the iPhone app comes first.
var lightroomBundleIDs = []string{"com.adobe.lrmobilephone", "com.adobe.lrmobile"}

var rootCmd = &cobra.Command{
	Use:           "lrpush",
	Short:         "Mirror an iPhone/iPad Lightroom app's userStyles to ./sync and live-sync edits back",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          func(cmd *cobra.Command, args []string) error { return run() },
}

func Execute() error { return rootCmd.Execute() }

func run() error {
	// 1. Pick device.
	infos, err := device.List()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		return fmt.Errorf("no USB device found; connect and trust your device")
	}
	chosen := infos[0]
	if len(infos) > 1 {
		labels := make([]string, len(infos))
		for i, d := range infos {
			labels[i] = fmt.Sprintf("%s  (%s, iOS %s, %s)", d.Name, d.ProductType, d.Version, d.UDID)
		}
		idx, err := pickIndex("Select a device", labels)
		if err != nil {
			return err
		}
		chosen = infos[idx]
	}

	// 2. Detect every installed Lightroom app.
	sessions, err := device.DetectSessions(chosen.UDID, lightroomBundleIDs)
	if err != nil {
		return err
	}
	defer func() {
		for _, s := range sessions {
			s.Close()
		}
	}()

	// 3. Per-app: report what would be mounted (mount flow lands next).
	for _, s := range sessions {
		docs, err := locate.DocumentsRoot(s.FS, "")
		if err != nil {
			return fmt.Errorf("[%s] %w", s.BundleID, err)
		}
		cands, err := locate.FindCatalogs(s.FS, docs)
		if err != nil {
			return fmt.Errorf("[%s] %w", s.BundleID, err)
		}
		fmt.Printf("[%s] Documents root %q, %d catalog(s)\n", s.BundleID, docs, len(cands))
	}
	fmt.Println("NFS mount flow lands in a later commit on this branch.")
	return nil
}
