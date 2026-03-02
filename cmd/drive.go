package cmd

import (
	"fmt"

	"github.com/jhansen317/keres/internal/auth"
	"github.com/jhansen317/keres/internal/drive"
	"github.com/spf13/cobra"
)

var driveCmd = &cobra.Command{
	Use:   "drive",
	Short: "Google Drive cleanup and organization tools",
	Long:  `Analyze and clean up your Google Drive to reclaim storage space.`,
}

var driveAnalyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyze Google Drive storage usage",
	Long: `Analyze your Google Drive to identify:
- Largest files
- Duplicate files
- Old files not accessed recently
- Files in trash
- Storage usage by file type`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := auth.GetGoogleClient([]string{"https://www.googleapis.com/auth/drive.readonly"})
		if err != nil {
			return fmt.Errorf("authentication required: run 'keres auth login'")
		}

		return drive.Analyze(client)
	},
}

var driveDuplicatesCmd = &cobra.Command{
	Use:   "find-duplicates",
	Short: "Find duplicate files",
	Long:  `Scan Google Drive for duplicate files based on name, size, and/or content hash.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := auth.GetGoogleClient([]string{"https://www.googleapis.com/auth/drive.readonly"})
		if err != nil {
			return fmt.Errorf("authentication required: run 'keres auth login'")
		}

		method, _ := cmd.Flags().GetString("method")
		return drive.FindDuplicates(client, method)
	},
}

var driveCleanTrashCmd = &cobra.Command{
	Use:   "empty-trash",
	Short: "Empty Google Drive trash",
	Long:  `Permanently delete all files in the Google Drive trash.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := auth.GetGoogleClient([]string{"https://www.googleapis.com/auth/drive"})
		if err != nil {
			return fmt.Errorf("authentication required: run 'keres auth login'")
		}

		dryRun, _ := cmd.Flags().GetBool("dry-run")
		return drive.EmptyTrash(client, dryRun)
	},
}

var driveLargeFilesCmd = &cobra.Command{
	Use:   "large-files",
	Short: "Find largest files in Drive",
	Long:  `List the largest files in your Google Drive to identify space hogs.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := auth.GetGoogleClient([]string{"https://www.googleapis.com/auth/drive.readonly"})
		if err != nil {
			return fmt.Errorf("authentication required: run 'keres auth login'")
		}

		limit, _ := cmd.Flags().GetInt("limit")
		minSize, _ := cmd.Flags().GetString("min-size")
		return drive.FindLargeFiles(client, limit, minSize)
	},
}

var driveOldFilesCmd = &cobra.Command{
	Use:   "old-files",
	Short: "Find files not accessed recently",
	Long:  `Find files that haven't been accessed in a specified number of days.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := auth.GetGoogleClient([]string{"https://www.googleapis.com/auth/drive.readonly"})
		if err != nil {
			return fmt.Errorf("authentication required: run 'keres auth login'")
		}

		days, _ := cmd.Flags().GetInt("days")
		return drive.FindOldFiles(client, days)
	},
}

func init() {
	rootCmd.AddCommand(driveCmd)
	driveCmd.AddCommand(driveAnalyzeCmd)
	driveCmd.AddCommand(driveDuplicatesCmd)
	driveCmd.AddCommand(driveCleanTrashCmd)
	driveCmd.AddCommand(driveLargeFilesCmd)
	driveCmd.AddCommand(driveOldFilesCmd)

	// Duplicates flags
	driveDuplicatesCmd.Flags().String("method", "name-size", "Detection method: name, name-size, or hash")

	// Trash flags
	driveCleanTrashCmd.Flags().Bool("dry-run", true, "Preview what would be deleted")

	// Large files flags
	driveLargeFilesCmd.Flags().Int("limit", 50, "Number of files to show")
	driveLargeFilesCmd.Flags().String("min-size", "100MB", "Minimum file size to include")

	// Old files flags
	driveOldFilesCmd.Flags().Int("days", 365, "Find files not accessed in this many days")
}
