package main

import "github.com/spf13/cobra"

var (
	flagUDID       string
	flagBundleID   string
	flagPathPrefix string
)

// lightroomBundleIDs are tried in order when --bundle-id is not set explicitly.
// Lightroom mobile uses a different bundle id on iPhone vs iPad/universal.
var lightroomBundleIDs = []string{"com.adobe.lrmobile", "com.adobe.lrmobilephone"}

// bundleCandidates returns the bundle id(s) to try: the explicit --bundle-id if
// the user set it, otherwise the known Lightroom ids for auto-detection.
func bundleCandidates(cmd *cobra.Command) []string {
	if cmd.Flags().Changed("bundle-id") {
		return []string{flagBundleID}
	}
	return lightroomBundleIDs
}

var rootCmd = &cobra.Command{
	Use:           "lrpush",
	Short:         "Push Lightroom presets to an iPhone's Lightroom mobile app over USB",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flagUDID, "udid", "", "target device udid (default: first USB device)")
	pf.StringVar(&flagBundleID, "bundle-id", "com.adobe.lrmobile", "app bundle id (if unset, auto-tries com.adobe.lrmobile and com.adobe.lrmobilephone)")
	pf.StringVar(&flagPathPrefix, "path-prefix", "", "override AFC root prefix (e.g. Documents)")

	rootCmd.AddCommand(newDevicesCmd(), newInspectCmd(), newPushCmd(), newRmCmd())
}

func Execute() error { return rootCmd.Execute() }
