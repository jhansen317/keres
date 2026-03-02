package cmd

import (
	"fmt"

	"github.com/jhansen317/keres/internal/icloud"
	"github.com/spf13/cobra"
)

var icloudCmd = &cobra.Command{
	Use:   "icloud",
	Short: "iCloud Drive cleanup and organization tools",
	Long:  `Analyze and clean up your iCloud Drive to reclaim storage space.`,
}

var icloudAnalyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyze iCloud Drive storage usage",
	Long: `Analyze your iCloud Drive to identify:
- Largest files
- File type breakdown
- Old files not modified recently
- Empty directories
- Storage usage statistics`,
	RunE: func(cmd *cobra.Command, args []string) error {
		stats, err := icloud.AnalyzeDrive()
		if err != nil {
			return err
		}

		icloud.PrintStats(stats)
		return nil
	},
}

var icloudDuplicatesCmd = &cobra.Command{
	Use:   "find-duplicates",
	Short: "Find duplicate files in iCloud Drive",
	Long:  `Scan iCloud Drive for duplicate files based on content hash (MD5).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		duplicates, err := icloud.FindDuplicates()
		if err != nil {
			return err
		}

		if len(duplicates) == 0 {
			fmt.Println("No duplicates found!")
			return nil
		}

		fmt.Printf("Found %d groups of duplicate files:\n\n", len(duplicates))

		totalWasted := int64(0)
		for _, files := range duplicates {
			fmt.Printf("Duplicate group (%d files):\n", len(files))
			for i, f := range files {
				fmt.Printf("  %d. %s (%s)\n", i+1, f.RelativePath, formatBytes(f.Size))
				if i > 0 {
					totalWasted += f.Size
				}
			}
			fmt.Println()
		}

		fmt.Printf("Potential space savings: %s\n", formatBytes(totalWasted))
		return nil
	},
}

var icloudLargeFilesCmd = &cobra.Command{
	Use:   "large-files",
	Short: "Find largest files in iCloud Drive",
	Long:  `List the largest files in your iCloud Drive to identify space hogs.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		minSizeStr, _ := cmd.Flags().GetString("min-size")
		limit, _ := cmd.Flags().GetInt("limit")

		minSize, err := parseSize(minSizeStr)
		if err != nil {
			return fmt.Errorf("invalid size: %w", err)
		}

		files, err := icloud.FindLargeFiles(minSize, limit)
		if err != nil {
			return err
		}

		if len(files) == 0 {
			fmt.Printf("No files larger than %s found.\n", minSizeStr)
			return nil
		}

		fmt.Printf("Found %d files larger than %s:\n\n", len(files), minSizeStr)
		for i, f := range files {
			fmt.Printf("%d. %s - %s\n", i+1, formatBytes(f.Size), f.RelativePath)
		}

		return nil
	},
}

var icloudOldFilesCmd = &cobra.Command{
	Use:   "old-files",
	Short: "Find files not modified recently",
	Long:  `Find files in iCloud Drive that haven't been modified in a specified number of days.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		days, _ := cmd.Flags().GetInt("days")
		limit, _ := cmd.Flags().GetInt("limit")

		files, err := icloud.FindOldFiles(days, limit)
		if err != nil {
			return err
		}

		if len(files) == 0 {
			fmt.Printf("No files older than %d days found.\n", days)
			return nil
		}

		totalSize := int64(0)
		for _, f := range files {
			totalSize += f.Size
		}

		fmt.Printf("Found %d files not modified in %d days (%s total):\n\n",
			len(files), days, formatBytes(totalSize))

		for i, f := range files {
			fmt.Printf("%d. %s - %s (last modified: %s)\n",
				i+1, formatBytes(f.Size), f.RelativePath, f.ModTime.Format("2006-01-02"))
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(icloudCmd)
	icloudCmd.AddCommand(icloudAnalyzeCmd)
	icloudCmd.AddCommand(icloudDuplicatesCmd)
	icloudCmd.AddCommand(icloudLargeFilesCmd)
	icloudCmd.AddCommand(icloudOldFilesCmd)

	// Large files flags
	icloudLargeFilesCmd.Flags().String("min-size", "100MB", "Minimum file size")
	icloudLargeFilesCmd.Flags().Int("limit", 50, "Number of files to show")

	// Old files flags
	icloudOldFilesCmd.Flags().Int("days", 730, "Find files older than this many days")
	icloudOldFilesCmd.Flags().Int("limit", 100, "Number of files to show")
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func parseSize(sizeStr string) (int64, error) {
	// Implementation in gmail.go can be reused via a shared utility
	// For now, simple implementation
	var num float64
	var unit string
	_, err := fmt.Sscanf(sizeStr, "%f%s", &num, &unit)
	if err != nil {
		return 0, err
	}

	multipliers := map[string]int64{
		"B":  1,
		"KB": 1024,
		"MB": 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
	}

	mult, ok := multipliers[unit]
	if !ok {
		return 0, fmt.Errorf("unknown unit: %s", unit)
	}

	return int64(num * float64(mult)), nil
}
