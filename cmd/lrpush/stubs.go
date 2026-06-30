package main

import "github.com/spf13/cobra"

func newRmCmd() *cobra.Command { return &cobra.Command{Use: "rm"} }
