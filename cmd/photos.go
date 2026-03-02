package cmd

import (
	"fmt"

	"github.com/jhansen317/keres/internal/photos"
	"github.com/spf13/cobra"
)

var photosCmd = &cobra.Command{
	Use:   "photos",
	Short: "Photos library analysis and organization tools",
	Long:  `Analyze your Photos library and organize photos using semantic search.`,
}

var photosAnalyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyze Photos library storage usage",
	Long: `Analyze your Photos library to identify:
- Total photos and videos
- Storage usage by year and type
- Largest photos/videos
- Potential duplicates
- Hidden and favorite counts`,
	RunE: func(cmd *cobra.Command, args []string) error {
		stats, err := photos.AnalyzeLibrary()
		if err != nil {
			return err
		}

		photos.PrintStats(stats)
		return nil
	},
}

var photosLargestCmd = &cobra.Command{
	Use:   "largest",
	Short: "Find largest photos and videos",
	Long:  `List the largest photos and videos in your library.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")

		items, err := photos.GetLargestPhotos(limit)
		if err != nil {
			return err
		}

		fmt.Printf("Top %d Largest Items:\n\n", len(items))
		for i, item := range items {
			typeStr := item.MediaType
			if item.MediaType == "Video" {
				typeStr = fmt.Sprintf("Video (%.1fs)", item.Duration)
			}
			fmt.Printf("%d. %s - %s - %s\n",
				i+1,
				formatBytesPhotos(item.OriginalSize),
				typeStr,
				item.Filename)
		}

		return nil
	},
}

var photosSearchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search photos by description (semantic search)",
	Long: `Search for photos using natural language descriptions.
Examples:
  keres photos search "beach sunset"
  keres photos search "photos of my dog"
  keres photos search "food pictures"

Note: This feature requires the ML service to be running.
      Run: cd ml_service && python app.py`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]
		limit, _ := cmd.Flags().GetInt("limit")

		fmt.Printf("Searching for: %s\n", query)
		fmt.Println("\nSemantic search requires the ML service.")
		fmt.Println("To enable this feature:")
		fmt.Println("  1. cd ml_service")
		fmt.Println("  2. pip install -r requirements.txt")
		fmt.Println("  3. python app.py")
		fmt.Println("\nSee docs/ICLOUD_DESIGN.md for details.")

		// TODO: Implement API call to ML service
		_ = limit
		_ = query

		return nil
	},
}

var photosIndexCmd = &cobra.Command{
	Use:   "index",
	Short: "Generate embeddings for all photos",
	Long: `Generate CLIP embeddings for all photos in your library.
This is required before using semantic search.

Note: This can take several hours for large libraries (10k+ photos).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Generating embeddings for all photos...")
		fmt.Println("\nThis feature requires the ML service.")
		fmt.Println("To enable this feature:")
		fmt.Println("  1. cd ml_service")
		fmt.Println("  2. pip install -r requirements.txt")
		fmt.Println("  3. python app.py")
		fmt.Println("\nSee docs/ICLOUD_DESIGN.md for details.")

		// TODO: Implement API call to ML service
		return nil
	},
}

func init() {
	rootCmd.AddCommand(photosCmd)
	photosCmd.AddCommand(photosAnalyzeCmd)
	photosCmd.AddCommand(photosLargestCmd)
	photosCmd.AddCommand(photosSearchCmd)
	photosCmd.AddCommand(photosIndexCmd)

	// Largest flags
	photosLargestCmd.Flags().Int("limit", 50, "Number of items to show")

	// Search flags
	photosSearchCmd.Flags().Int("limit", 20, "Number of results to return")
}

func formatBytesPhotos(bytes int64) string {
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
