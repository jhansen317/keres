package drive

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type DriveStats struct {
	TotalFiles     int
	TotalSize      int64
	FilesByType    map[string]int
	LargestFiles   []FileInfo
	TrashCount     int
	TrashSize      int64
	OldFilesCount  int
	DuplicateFiles map[string][]FileInfo
}

type FileInfo struct {
	ID           string
	Name         string
	Size         int64
	MimeType     string
	CreatedTime  string
	ModifiedTime string
	WebViewLink  string
	Trashed      bool
}

// Analyze performs a comprehensive analysis of Google Drive
func Analyze(client *http.Client) error {
	ctx := context.Background()
	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Drive service: %w", err)
	}

	fmt.Println("Analyzing your Google Drive...")
	fmt.Println("This may take a few minutes for large drives.\n")

	// Get storage quota
	about, err := srv.About.Get().Fields("storageQuota, user").Do()
	if err != nil {
		return fmt.Errorf("unable to get storage info: %w", err)
	}

	fmt.Printf("User: %s\n", about.User.EmailAddress)
	fmt.Printf("Storage used: %s / %s (%.1f%%)\n",
		formatBytes(about.StorageQuota.Usage),
		formatBytes(about.StorageQuota.Limit),
		float64(about.StorageQuota.Usage)/float64(about.StorageQuota.Limit)*100)
	fmt.Printf("Drive usage: %s\n", formatBytes(about.StorageQuota.UsageInDrive))
	fmt.Printf("Gmail usage: %s\n", formatBytes(about.StorageQuota.UsageInDriveTrash))
	fmt.Printf("Photos usage: %s\n\n", formatBytes(about.StorageQuota.UsageInDrive-about.StorageQuota.UsageInDriveTrash))

	stats := analyzeFiles(srv)
	printDriveStats(stats)

	return nil
}

func analyzeFiles(srv *drive.Service) *DriveStats {
	stats := &DriveStats{
		FilesByType:    make(map[string]int),
		LargestFiles:   make([]FileInfo, 0),
		DuplicateFiles: make(map[string][]FileInfo),
	}

	pageToken := ""
	for {
		req := srv.Files.List().
			PageSize(1000).
			Fields("nextPageToken, files(id, name, size, mimeType, createdTime, modifiedTime, webViewLink, trashed)").
			PageToken(pageToken)

		res, err := req.Do()
		if err != nil {
			fmt.Printf("Error listing files: %v\n", err)
			break
		}

		for _, f := range res.Files {
			stats.TotalFiles++

			size := f.Size
			if size > 0 {
				stats.TotalSize += size
			}

			// Track by type
			mimeType := simplifyMimeType(f.MimeType)
			stats.FilesByType[mimeType]++

			// Track trashed files
			if f.Trashed {
				stats.TrashCount++
				stats.TrashSize += size
			}

			// Track largest files
			info := FileInfo{
				ID:           f.Id,
				Name:         f.Name,
				Size:         size,
				MimeType:     f.MimeType,
				CreatedTime:  f.CreatedTime,
				ModifiedTime: f.ModifiedTime,
				WebViewLink:  f.WebViewLink,
				Trashed:      f.Trashed,
			}
			stats.LargestFiles = append(stats.LargestFiles, info)
		}

		fmt.Printf("\rProcessed %d files...", stats.TotalFiles)

		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}

	fmt.Println()
	return stats
}

func printDriveStats(stats *DriveStats) {
	fmt.Println("\n=== DRIVE ANALYSIS RESULTS ===\n")
	fmt.Printf("Total files: %d\n", stats.TotalFiles)
	fmt.Printf("Total size: %s\n", formatBytes(stats.TotalSize))
	fmt.Printf("Files in trash: %d (%s)\n\n", stats.TrashCount, formatBytes(stats.TrashSize))

	// Sort and show file types
	type typeCount struct {
		Type  string
		Count int
	}
	types := make([]typeCount, 0, len(stats.FilesByType))
	for t, count := range stats.FilesByType {
		types = append(types, typeCount{t, count})
	}
	sort.Slice(types, func(i, j int) bool {
		return types[i].Count > types[j].Count
	})

	fmt.Println("Files by Type:")
	for i := 0; i < min(10, len(types)); i++ {
		fmt.Printf("  %s: %d\n", types[i].Type, types[i].Count)
	}

	// Sort and show largest files
	sort.Slice(stats.LargestFiles, func(i, j int) bool {
		return stats.LargestFiles[i].Size > stats.LargestFiles[j].Size
	})

	fmt.Println("\nTop 20 Largest Files:")
	for i := 0; i < min(20, len(stats.LargestFiles)); i++ {
		f := stats.LargestFiles[i]
		if f.Size > 0 {
			trashed := ""
			if f.Trashed {
				trashed = " [TRASH]"
			}
			fmt.Printf("  %d. %s - %s%s\n", i+1, formatBytes(f.Size), truncate(f.Name, 60), trashed)
		}
	}
}

// FindDuplicates finds duplicate files in Drive
func FindDuplicates(client *http.Client, method string) error {
	ctx := context.Background()
	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Drive service: %w", err)
	}

	fmt.Printf("Scanning for duplicates using method: %s\n", method)

	fileMap := make(map[string][]FileInfo)
	pageToken := ""

	for {
		req := srv.Files.List().
			PageSize(1000).
			Fields("nextPageToken, files(id, name, size, md5Checksum, webViewLink)").
			PageToken(pageToken)

		res, err := req.Do()
		if err != nil {
			return fmt.Errorf("error listing files: %w", err)
		}

		for _, f := range res.Files {
			var key string
			switch method {
			case "name":
				key = f.Name
			case "name-size":
				key = fmt.Sprintf("%s:%d", f.Name, f.Size)
			case "hash":
				if f.Md5Checksum != "" {
					key = f.Md5Checksum
				} else {
					continue
				}
			default:
				return fmt.Errorf("unknown method: %s", method)
			}

			info := FileInfo{
				ID:          f.Id,
				Name:        f.Name,
				Size:        f.Size,
				WebViewLink: f.WebViewLink,
			}
			fileMap[key] = append(fileMap[key], info)
		}

		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}

	// Find duplicates
	duplicateCount := 0
	duplicateSize := int64(0)
	for key, files := range fileMap {
		if len(files) > 1 {
			duplicateCount++
			fmt.Printf("\nDuplicate group (key: %s):\n", truncate(key, 50))
			for i, f := range files {
				fmt.Printf("  %d. %s (%s)\n", i+1, f.Name, formatBytes(f.Size))
				fmt.Printf("     %s\n", f.WebViewLink)
				if i > 0 {
					duplicateSize += f.Size
				}
			}
		}
	}

	fmt.Printf("\nFound %d duplicate groups\n", duplicateCount)
	fmt.Printf("Potential space savings: %s\n", formatBytes(duplicateSize))

	return nil
}

// EmptyTrash permanently deletes all files in trash
func EmptyTrash(client *http.Client, dryRun bool) error {
	ctx := context.Background()
	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Drive service: %w", err)
	}

	// List trashed files
	var trashedFiles []*drive.File
	pageToken := ""

	for {
		req := srv.Files.List().
			Q("trashed=true").
			PageSize(1000).
			Fields("nextPageToken, files(id, name, size)").
			PageToken(pageToken)

		res, err := req.Do()
		if err != nil {
			return fmt.Errorf("error listing trashed files: %w", err)
		}

		trashedFiles = append(trashedFiles, res.Files...)

		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}

	totalSize := int64(0)
	for _, f := range trashedFiles {
		totalSize += f.Size
	}

	fmt.Printf("Found %d files in trash (%s)\n", len(trashedFiles), formatBytes(totalSize))

	if len(trashedFiles) == 0 {
		fmt.Println("Trash is already empty!")
		return nil
	}

	if dryRun {
		fmt.Println("\nDRY RUN - No files will be deleted")
		fmt.Printf("Would permanently delete %d files\n", len(trashedFiles))
		return nil
	}

	fmt.Println("Emptying trash...")
	if err := srv.Files.EmptyTrash().Do(); err != nil {
		return fmt.Errorf("failed to empty trash: %w", err)
	}

	fmt.Println("Trash emptied.")
	return nil
}

// FindLargeFiles finds the largest files in Drive
func FindLargeFiles(client *http.Client, limit int, minSizeStr string) error {
	ctx := context.Background()
	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Drive service: %w", err)
	}

	minSize, err := parseSize(minSizeStr)
	if err != nil {
		return fmt.Errorf("invalid size format: %w", err)
	}

	fmt.Printf("Finding files larger than %s...\n", minSizeStr)

	var largeFiles []FileInfo
	pageToken := ""

	for {
		req := srv.Files.List().
			PageSize(1000).
			Fields("nextPageToken, files(id, name, size, mimeType, modifiedTime, webViewLink)").
			PageToken(pageToken)

		res, err := req.Do()
		if err != nil {
			return fmt.Errorf("error listing files: %w", err)
		}

		for _, f := range res.Files {
			if f.Size >= minSize {
				largeFiles = append(largeFiles, FileInfo{
					ID:           f.Id,
					Name:         f.Name,
					Size:         f.Size,
					MimeType:     f.MimeType,
					ModifiedTime: f.ModifiedTime,
					WebViewLink:  f.WebViewLink,
				})
			}
		}

		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}

	// Sort by size
	sort.Slice(largeFiles, func(i, j int) bool {
		return largeFiles[i].Size > largeFiles[j].Size
	})

	fmt.Printf("\nFound %d files larger than %s\n\n", len(largeFiles), minSizeStr)

	for i := 0; i < min(limit, len(largeFiles)); i++ {
		f := largeFiles[i]
		fmt.Printf("%d. %s - %s\n", i+1, formatBytes(f.Size), f.Name)
		fmt.Printf("   Type: %s | Modified: %s\n", simplifyMimeType(f.MimeType), f.ModifiedTime)
		fmt.Printf("   %s\n\n", f.WebViewLink)
	}

	return nil
}

// FindOldFiles finds files not accessed in a while
func FindOldFiles(client *http.Client, days int) error {
	ctx := context.Background()
	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Drive service: %w", err)
	}

	cutoffDate := time.Now().AddDate(0, 0, -days)
	query := fmt.Sprintf("modifiedTime < '%s' and trashed=false", cutoffDate.Format(time.RFC3339))

	fmt.Printf("Finding files not modified in %d days...\n", days)

	var oldFiles []FileInfo
	pageToken := ""
	totalSize := int64(0)

	for {
		req := srv.Files.List().
			Q(query).
			PageSize(1000).
			Fields("nextPageToken, files(id, name, size, modifiedTime, webViewLink)").
			PageToken(pageToken)

		res, err := req.Do()
		if err != nil {
			return fmt.Errorf("error listing files: %w", err)
		}

		for _, f := range res.Files {
			oldFiles = append(oldFiles, FileInfo{
				ID:           f.Id,
				Name:         f.Name,
				Size:         f.Size,
				ModifiedTime: f.ModifiedTime,
				WebViewLink:  f.WebViewLink,
			})
			totalSize += f.Size
		}

		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}

	// Sort by size
	sort.Slice(oldFiles, func(i, j int) bool {
		return oldFiles[i].Size > oldFiles[j].Size
	})

	fmt.Printf("\nFound %d old files (%s total)\n\n", len(oldFiles), formatBytes(totalSize))

	for i := 0; i < min(50, len(oldFiles)); i++ {
		f := oldFiles[i]
		fmt.Printf("%d. %s - %s\n", i+1, formatBytes(f.Size), f.Name)
		fmt.Printf("   Last modified: %s\n", f.ModifiedTime)
		fmt.Printf("   %s\n\n", f.WebViewLink)
	}

	return nil
}

// Helper functions

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

func simplifyMimeType(mimeType string) string {
	parts := strings.Split(mimeType, "/")
	if len(parts) == 2 {
		return parts[1]
	}
	return mimeType
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func parseSize(sizeStr string) (int64, error) {
	sizeStr = strings.ToUpper(strings.TrimSpace(sizeStr))
	multipliers := map[string]int64{
		"B":  1,
		"KB": 1024,
		"MB": 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
	}

	for suffix, mult := range multipliers {
		if strings.HasSuffix(sizeStr, suffix) {
			numStr := strings.TrimSuffix(sizeStr, suffix)
			var num float64
			_, err := fmt.Sscanf(numStr, "%f", &num)
			if err != nil {
				return 0, err
			}
			return int64(num * float64(mult)), nil
		}
	}

	return 0, fmt.Errorf("invalid size format: %s (use B, KB, MB, or GB)", sizeStr)
}
