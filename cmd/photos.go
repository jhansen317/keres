package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jhansen317/keres/internal/photos"
	"github.com/spf13/cobra"
)

const mlServiceURL = "http://localhost:5001"

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

Requires the ML service to be running: cd ml_service && python app.py`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]
		limit, _ := cmd.Flags().GetInt("limit")

		if err := checkMLService(); err != nil {
			return err
		}

		fmt.Printf("Searching for: %s\n\n", query)

		body := fmt.Sprintf(`{"query": %q, "limit": %d}`, query, limit)
		resp, err := http.Post(mlServiceURL+"/search", "application/json", strings.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to call ML service: %w", err)
		}
		defer resp.Body.Close()

		var result struct {
			Query        string `json:"query"`
			TotalIndexed int    `json:"total_indexed"`
			Message      string `json:"message"`
			Results      []struct {
				UUID  string  `json:"uuid"`
				Path  string  `json:"path"`
				Score float64 `json:"score"`
			} `json:"results"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}

		if result.Message != "" {
			fmt.Println(result.Message)
			return nil
		}

		fmt.Printf("Results (%d indexed photos searched):\n\n", result.TotalIndexed)
		for i, r := range result.Results {
			scoreBar := renderScore(r.Score)
			fmt.Printf("%2d. %s %.3f  %s\n", i+1, scoreBar, r.Score, r.Path)
		}

		return nil
	},
}

var photosIndexCmd = &cobra.Command{
	Use:   "index",
	Short: "Generate embeddings for all photos",
	Long: `Generate CLIP embeddings for all photos in your library.
This is required before using semantic search.

Requires the ML service to be running: cd ml_service && python app.py

Already-indexed photos are skipped by default. Use --reindex to re-process everything.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reindex, _ := cmd.Flags().GetBool("reindex")

		if err := checkMLService(); err != nil {
			return err
		}

		// Start indexing
		skipIndexed := !reindex
		body := fmt.Sprintf(`{"skip_indexed": %t}`, skipIndexed)
		resp, err := http.Post(mlServiceURL+"/index/all", "application/json", strings.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to start indexing: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusConflict {
			fmt.Println("Indexing is already in progress.")
			fmt.Println("Run 'keres photos index --status' to check progress.")
			return nil
		}

		var startResult struct {
			Status         string `json:"status"`
			TotalInLibrary int    `json:"total_in_library"`
			AlreadyIndexed int    `json:"already_indexed"`
			Error          string `json:"error"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&startResult); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}

		if startResult.Error != "" {
			return fmt.Errorf("indexing error: %s", startResult.Error)
		}

		fmt.Printf("Indexing started\n")
		fmt.Printf("  Photos in library: %d\n", startResult.TotalInLibrary)
		if skipIndexed && startResult.AlreadyIndexed > 0 {
			fmt.Printf("  Already indexed:   %d (skipping)\n", startResult.AlreadyIndexed)
		}
		fmt.Println()

		// Poll for progress
		return pollIndexStatus()
	},
}

func pollIndexStatus() error {
	client := &http.Client{Timeout: 5 * time.Second}

	for {
		time.Sleep(2 * time.Second)

		resp, err := client.Get(mlServiceURL + "/index/status")
		if err != nil {
			fmt.Printf("\rFailed to get status: %v", err)
			continue
		}

		var status struct {
			Running        bool    `json:"running"`
			Total          int     `json:"total"`
			Indexed        int     `json:"indexed"`
			Skipped        int     `json:"skipped"`
			Failed         int     `json:"failed"`
			CurrentFile    string  `json:"current_file"`
			ElapsedSeconds float64 `json:"elapsed_seconds"`
			ImagesPerSec   float64 `json:"images_per_second"`
			Error          string  `json:"error"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		if status.Error != "" {
			return fmt.Errorf("indexing failed: %s", status.Error)
		}

		if status.Total > 0 {
			pct := float64(status.Indexed) / float64(status.Total) * 100
			rateStr := ""
			if status.ImagesPerSec > 0 {
				remaining := float64(status.Total-status.Indexed) / status.ImagesPerSec
				rateStr = fmt.Sprintf("  %.1f img/s  ~%s remaining", status.ImagesPerSec, formatDuration(remaining))
			}
			fmt.Printf("\r  [%3.0f%%] %d/%d indexed, %d failed%s   ",
				pct, status.Indexed, status.Total, status.Failed, rateStr)
		} else {
			fmt.Printf("\r  Discovering photos...   ")
		}

		if !status.Running {
			fmt.Printf("\n\nIndexing complete!\n")
			fmt.Printf("  Indexed: %d\n", status.Indexed)
			if status.Skipped > 0 {
				fmt.Printf("  Skipped: %d (already indexed)\n", status.Skipped)
			}
			if status.Failed > 0 {
				fmt.Printf("  Failed:  %d\n", status.Failed)
			}
			if status.ElapsedSeconds > 0 {
				fmt.Printf("  Time:    %s\n", formatDuration(status.ElapsedSeconds))
			}
			return nil
		}
	}
}

func checkMLService() error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(mlServiceURL + "/health")
	if err != nil {
		return fmt.Errorf("ML service is not running at %s\n\n"+
			"Start it with:\n"+
			"  cd ml_service && python app.py", mlServiceURL)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

func renderScore(score float64) string {
	filled := int(score * 10)
	if filled < 0 {
		filled = 0
	}
	if filled > 10 {
		filled = 10
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat(" ", 10-filled) + "]"
}

func formatDuration(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func init() {
	rootCmd.AddCommand(photosCmd)
	photosCmd.AddCommand(photosAnalyzeCmd)
	photosCmd.AddCommand(photosLargestCmd)
	photosCmd.AddCommand(photosSearchCmd)
	photosCmd.AddCommand(photosIndexCmd)

	photosLargestCmd.Flags().Int("limit", 50, "Number of items to show")
	photosSearchCmd.Flags().Int("limit", 20, "Number of results to return")
	photosIndexCmd.Flags().Bool("reindex", false, "Re-index all photos (ignore existing embeddings)")
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
