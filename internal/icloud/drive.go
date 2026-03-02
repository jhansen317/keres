package icloud

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	iCloudDrivePath = "Library/Mobile Documents/com~apple~CloudDocs"
)

type FileInfo struct {
	Path         string
	Name         string
	Size         int64
	ModTime      time.Time
	IsDir        bool
	RelativePath string
}

type DriveStats struct {
	TotalFiles     int
	TotalDirs      int
	TotalSize      int64
	LargestFiles   []FileInfo
	FilesByExt     map[string]int
	SizeByExt      map[string]int64
	OldFiles       []FileInfo
	Duplicates     map[string][]FileInfo
	EmptyDirs      []string
}

// GetICloudDrivePath returns the full path to iCloud Drive
func GetICloudDrivePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	icloudPath := filepath.Join(home, iCloudDrivePath)

	// Check if iCloud Drive is enabled
	if _, err := os.Stat(icloudPath); os.IsNotExist(err) {
		return "", fmt.Errorf("iCloud Drive not found at %s\n"+
			"Make sure iCloud Drive is enabled in System Preferences", icloudPath)
	}

	return icloudPath, nil
}

// Analyze performs comprehensive analysis of iCloud Drive
func AnalyzeDrive() (*DriveStats, error) {
	icloudPath, err := GetICloudDrivePath()
	if err != nil {
		return nil, err
	}

	stats := &DriveStats{
		LargestFiles: make([]FileInfo, 0),
		FilesByExt:   make(map[string]int),
		SizeByExt:    make(map[string]int64),
		OldFiles:     make([]FileInfo, 0),
		Duplicates:   make(map[string][]FileInfo),
		EmptyDirs:    make([]string, 0),
	}

	fmt.Printf("Scanning iCloud Drive: %s\n\n", icloudPath)

	err = filepath.Walk(icloudPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip files we can't access
			return nil
		}

		// Skip iCloud metadata files
		if filepath.Base(path) == ".DS_Store" ||
		   filepath.Ext(path) == ".icloud" {
			return nil
		}

		relPath, _ := filepath.Rel(icloudPath, path)

		fileInfo := FileInfo{
			Path:         path,
			Name:         info.Name(),
			Size:         info.Size(),
			ModTime:      info.ModTime(),
			IsDir:        info.IsDir(),
			RelativePath: relPath,
		}

		if info.IsDir() {
			stats.TotalDirs++

			// Check if directory is empty
			entries, err := os.ReadDir(path)
			if err == nil && len(entries) == 0 {
				stats.EmptyDirs = append(stats.EmptyDirs, relPath)
			}
		} else {
			stats.TotalFiles++
			stats.TotalSize += info.Size()

			// Track by extension
			ext := filepath.Ext(info.Name())
			if ext == "" {
				ext = "(no extension)"
			}
			stats.FilesByExt[ext]++
			stats.SizeByExt[ext] += info.Size()

			// Add to largest files
			stats.LargestFiles = append(stats.LargestFiles, fileInfo)

			// Track old files (not modified in 2 years)
			if time.Since(info.ModTime()) > 730*24*time.Hour {
				stats.OldFiles = append(stats.OldFiles, fileInfo)
			}
		}

		if stats.TotalFiles%100 == 0 {
			fmt.Printf("\rScanned %d files...", stats.TotalFiles)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error scanning iCloud Drive: %w", err)
	}

	fmt.Printf("\rScanned %d files.    \n\n", stats.TotalFiles)

	// Sort largest files
	sort.Slice(stats.LargestFiles, func(i, j int) bool {
		return stats.LargestFiles[i].Size > stats.LargestFiles[j].Size
	})

	// Sort old files by size
	sort.Slice(stats.OldFiles, func(i, j int) bool {
		return stats.OldFiles[i].Size > stats.OldFiles[j].Size
	})

	return stats, nil
}

// FindDuplicates finds duplicate files in iCloud Drive by content hash
func FindDuplicates() (map[string][]FileInfo, error) {
	icloudPath, err := GetICloudDrivePath()
	if err != nil {
		return nil, err
	}

	fmt.Println("Scanning for duplicate files (this may take a while)...\n")

	hashMap := make(map[string][]FileInfo)
	processed := 0

	err = filepath.Walk(icloudPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// Skip small files and metadata
		if info.Size() < 1024 || filepath.Ext(path) == ".icloud" {
			return nil
		}

		hash, err := hashFile(path)
		if err != nil {
			return nil // Skip files we can't hash
		}

		relPath, _ := filepath.Rel(icloudPath, path)
		fileInfo := FileInfo{
			Path:         path,
			Name:         info.Name(),
			Size:         info.Size(),
			ModTime:      info.ModTime(),
			RelativePath: relPath,
		}

		hashMap[hash] = append(hashMap[hash], fileInfo)
		processed++

		if processed%50 == 0 {
			fmt.Printf("\rProcessed %d files...", processed)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	fmt.Printf("\rProcessed %d files.    \n\n", processed)

	// Filter to only duplicates
	duplicates := make(map[string][]FileInfo)
	for hash, files := range hashMap {
		if len(files) > 1 {
			duplicates[hash] = files
		}
	}

	return duplicates, nil
}

// FindLargeFiles finds files larger than a specified size
func FindLargeFiles(minSizeBytes int64, limit int) ([]FileInfo, error) {
	icloudPath, err := GetICloudDrivePath()
	if err != nil {
		return nil, err
	}

	var largeFiles []FileInfo

	err = filepath.Walk(icloudPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		if info.Size() >= minSizeBytes {
			relPath, _ := filepath.Rel(icloudPath, path)
			largeFiles = append(largeFiles, FileInfo{
				Path:         path,
				Name:         info.Name(),
				Size:         info.Size(),
				ModTime:      info.ModTime(),
				RelativePath: relPath,
			})
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort by size
	sort.Slice(largeFiles, func(i, j int) bool {
		return largeFiles[i].Size > largeFiles[j].Size
	})

	if len(largeFiles) > limit {
		largeFiles = largeFiles[:limit]
	}

	return largeFiles, nil
}

// FindOldFiles finds files not modified in specified number of days
func FindOldFiles(days int, limit int) ([]FileInfo, error) {
	icloudPath, err := GetICloudDrivePath()
	if err != nil {
		return nil, err
	}

	cutoffDate := time.Now().AddDate(0, 0, -days)
	var oldFiles []FileInfo

	err = filepath.Walk(icloudPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		if info.ModTime().Before(cutoffDate) {
			relPath, _ := filepath.Rel(icloudPath, path)
			oldFiles = append(oldFiles, FileInfo{
				Path:         path,
				Name:         info.Name(),
				Size:         info.Size(),
				ModTime:      info.ModTime(),
				RelativePath: relPath,
			})
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort by size
	sort.Slice(oldFiles, func(i, j int) bool {
		return oldFiles[i].Size > oldFiles[j].Size
	})

	if len(oldFiles) > limit {
		oldFiles = oldFiles[:limit]
	}

	return oldFiles, nil
}

// Helper functions

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// PrintStats prints drive statistics
func PrintStats(stats *DriveStats) {
	fmt.Println("=== iCloud Drive Analysis ===\n")
	fmt.Printf("Total files: %d\n", stats.TotalFiles)
	fmt.Printf("Total directories: %d\n", stats.TotalDirs)
	fmt.Printf("Total size: %s\n\n", formatBytes(stats.TotalSize))

	// File types
	type extCount struct {
		Ext   string
		Count int
		Size  int64
	}
	exts := make([]extCount, 0, len(stats.FilesByExt))
	for ext, count := range stats.FilesByExt {
		exts = append(exts, extCount{ext, count, stats.SizeByExt[ext]})
	}
	sort.Slice(exts, func(i, j int) bool {
		return exts[i].Size > exts[j].Size
	})

	fmt.Println("Top File Types by Size:")
	for i := 0; i < min(10, len(exts)); i++ {
		fmt.Printf("  %s: %d files (%s)\n", exts[i].Ext, exts[i].Count, formatBytes(exts[i].Size))
	}

	// Largest files
	fmt.Println("\nTop 20 Largest Files:")
	for i := 0; i < min(20, len(stats.LargestFiles)); i++ {
		f := stats.LargestFiles[i]
		fmt.Printf("  %d. %s - %s\n", i+1, formatBytes(f.Size), truncate(f.RelativePath, 70))
	}

	// Old files
	if len(stats.OldFiles) > 0 {
		totalOldSize := int64(0)
		for _, f := range stats.OldFiles {
			totalOldSize += f.Size
		}
		fmt.Printf("\nOld Files (not modified in 2+ years): %d files (%s)\n",
			len(stats.OldFiles), formatBytes(totalOldSize))
	}

	// Empty directories
	if len(stats.EmptyDirs) > 0 {
		fmt.Printf("\nEmpty Directories: %d\n", len(stats.EmptyDirs))
		for i := 0; i < min(10, len(stats.EmptyDirs)); i++ {
			fmt.Printf("  - %s\n", stats.EmptyDirs[i])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
