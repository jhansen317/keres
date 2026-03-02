package photos

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	photosLibraryPath = "Pictures/Photos Library.photoslibrary"
	photosDatabasePath = "database/photos.db"
)

type PhotoInfo struct {
	UUID         string
	Filename     string
	Directory    string
	OriginalSize int64
	MediaType    string // Image, Video
	DateCreated  time.Time
	Width        int
	Height       int
	Duration     float64 // for videos
	IsFavorite   bool
	IsHidden     bool
}

type LibraryStats struct {
	TotalPhotos      int
	TotalVideos      int
	TotalSize        int64
	LargestItems     []PhotoInfo
	SizeByYear       map[int]int64
	SizeByMediaType  map[string]int64
	DuplicatesByName map[string][]PhotoInfo
	HiddenCount      int
	FavoriteCount    int
}

// GetPhotosLibraryPath returns the path to the Photos library database
func GetPhotosLibraryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	libraryPath := filepath.Join(home, photosLibraryPath)
	dbPath := filepath.Join(libraryPath, photosDatabasePath)

	// Check if Photos library exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return "", fmt.Errorf("Photos library database not found at %s\n"+
			"Make sure Photos.app is installed and has been opened at least once", dbPath)
	}

	return dbPath, nil
}

// AnalyzeLibrary performs comprehensive analysis of the Photos library
func AnalyzeLibrary() (*LibraryStats, error) {
	dbPath, err := GetPhotosLibraryPath()
	if err != nil {
		return nil, err
	}

	// Open database in read-only mode
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", dbPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open Photos database: %w", err)
	}
	defer db.Close()

	stats := &LibraryStats{
		LargestItems:     make([]PhotoInfo, 0),
		SizeByYear:       make(map[int]int64),
		SizeByMediaType:  make(map[string]int64),
		DuplicatesByName: make(map[string][]PhotoInfo),
	}

	fmt.Println("Analyzing Photos library...\n")

	// Query asset information
	// Note: Photos.app database schema can change between macOS versions
	// This query works on macOS 12+ (Monterey and later)
	query := `
		SELECT
			ZASSET.ZUUID,
			ZASSET.ZFILENAME,
			ZASSET.ZDIRECTORY,
			ZASSET.ZFILESIZE,
			CASE WHEN ZASSET.ZKIND = 0 THEN 'Image' ELSE 'Video' END as MediaType,
			datetime(ZASSET.ZDATECREATED + 978307200, 'unixepoch') as DateCreated,
			ZASSET.ZWIDTH,
			ZASSET.ZHEIGHT,
			ZASSET.ZDURATION,
			ZASSET.ZFAVORITE,
			ZASSET.ZHIDDEN
		FROM ZASSET
		WHERE ZASSET.ZTRASHEDSTATE = 0
	`

	rows, err := db.Query(query)
	if err != nil {
		// Try alternative query for older macOS versions
		query = `
			SELECT
				ZUUID,
				ZFILENAME,
				ZDIRECTORY,
				'',
				0,
				'',
				0,
				0,
				0,
				0,
				0
			FROM ZASSET
			LIMIT 1
		`
		rows, err = db.Query(query)
		if err != nil {
			return nil, fmt.Errorf("failed to query Photos database: %w\n"+
				"Note: Photos database schema varies by macOS version", err)
		}
		fmt.Println("Using limited query mode (older macOS version)")
	}
	defer rows.Close()

	for rows.Next() {
		var photo PhotoInfo
		var dateStr string
		var favorite, hidden int

		err := rows.Scan(
			&photo.UUID,
			&photo.Filename,
			&photo.Directory,
			&photo.OriginalSize,
			&photo.MediaType,
			&dateStr,
			&photo.Width,
			&photo.Height,
			&photo.Duration,
			&favorite,
			&hidden,
		)
		if err != nil {
			continue // Skip malformed rows
		}

		photo.IsFavorite = favorite == 1
		photo.IsHidden = hidden == 1

		// Parse date
		photo.DateCreated, _ = time.Parse("2006-01-02 15:04:05", dateStr)

		// Update stats
		if photo.MediaType == "Image" {
			stats.TotalPhotos++
		} else {
			stats.TotalVideos++
		}

		stats.TotalSize += photo.OriginalSize
		stats.SizeByMediaType[photo.MediaType] += photo.OriginalSize

		year := photo.DateCreated.Year()
		if year > 1970 { // Valid year
			stats.SizeByYear[year] += photo.OriginalSize
		}

		if photo.IsFavorite {
			stats.FavoriteCount++
		}
		if photo.IsHidden {
			stats.HiddenCount++
		}

		// Track largest items
		stats.LargestItems = append(stats.LargestItems, photo)

		// Track potential duplicates by filename
		stats.DuplicatesByName[photo.Filename] = append(stats.DuplicatesByName[photo.Filename], photo)
	}

	// Sort largest items
	sort.Slice(stats.LargestItems, func(i, j int) bool {
		return stats.LargestItems[i].OriginalSize > stats.LargestItems[j].OriginalSize
	})

	// Filter duplicates
	for filename, items := range stats.DuplicatesByName {
		if len(items) <= 1 {
			delete(stats.DuplicatesByName, filename)
		}
	}

	return stats, nil
}

// GetLargestPhotos returns the largest photos/videos
func GetLargestPhotos(limit int) ([]PhotoInfo, error) {
	stats, err := AnalyzeLibrary()
	if err != nil {
		return nil, err
	}

	if len(stats.LargestItems) > limit {
		return stats.LargestItems[:limit], nil
	}

	return stats.LargestItems, nil
}

// PrintStats prints library statistics
func PrintStats(stats *LibraryStats) {
	fmt.Println("=== Photos Library Analysis ===\n")
	fmt.Printf("Total photos: %d\n", stats.TotalPhotos)
	fmt.Printf("Total videos: %d\n", stats.TotalVideos)
	fmt.Printf("Total items: %d\n", stats.TotalPhotos+stats.TotalVideos)
	fmt.Printf("Total size: %s\n", formatBytes(stats.TotalSize))
	fmt.Printf("Favorites: %d\n", stats.FavoriteCount)
	fmt.Printf("Hidden: %d\n\n", stats.HiddenCount)

	// Size by media type
	fmt.Println("Storage by Media Type:")
	for mediaType, size := range stats.SizeByMediaType {
		fmt.Printf("  %s: %s\n", mediaType, formatBytes(size))
	}

	// Size by year
	type yearSize struct {
		Year int
		Size int64
	}
	years := make([]yearSize, 0, len(stats.SizeByYear))
	for year, size := range stats.SizeByYear {
		years = append(years, yearSize{year, size})
	}
	sort.Slice(years, func(i, j int) bool {
		return years[i].Year > years[j].Year
	})

	fmt.Println("\nStorage by Year:")
	for _, y := range years {
		if y.Year > 1970 {
			fmt.Printf("  %d: %s\n", y.Year, formatBytes(y.Size))
		}
	}

	// Largest items
	fmt.Println("\nTop 20 Largest Items:")
	for i := 0; i < min(20, len(stats.LargestItems)); i++ {
		item := stats.LargestItems[i]
		typeStr := item.MediaType
		if item.MediaType == "Video" {
			typeStr = fmt.Sprintf("Video (%.1fs)", item.Duration)
		}
		fmt.Printf("  %d. %s - %s - %s\n",
			i+1,
			formatBytes(item.OriginalSize),
			typeStr,
			item.Filename)
	}

	// Duplicates
	if len(stats.DuplicatesByName) > 0 {
		fmt.Printf("\nPotential Duplicates (same filename): %d\n", len(stats.DuplicatesByName))
		count := 0
		for filename, items := range stats.DuplicatesByName {
			if count >= 10 {
				break
			}
			fmt.Printf("  %s (%d copies)\n", filename, len(items))
			count++
		}
	}
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
