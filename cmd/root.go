package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "keres",
	Short: "Reclaim storage space from Gmail, Google Drive, and iCloud",
	Long: `Keres is a high-performance CLI tool for cleaning up and organizing
your cloud storage accounts. It helps you manage massive email backlogs,
remove duplicate files, and identify large space-consuming items.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().String("config", "", "config file (default is $HOME/.keres.yaml)")
}
