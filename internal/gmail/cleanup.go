package gmail

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type EmailStats struct {
	TotalEmails       int64
	UnreadEmails      int64
	TotalSize         int64
	LargestEmails     []EmailInfo
	TopSenders        map[string]int
	CategoryBreakdown map[string]int
}

type EmailInfo struct {
	ID      string
	Subject string
	From    string
	Date    string
	Size    int64
	Labels  []string
}

// Analyze performs a comprehensive analysis of the Gmail account
func Analyze(client *http.Client, limit int) error {
	ctx := context.Background()
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Gmail service: %w", err)
	}

	fmt.Println("Analyzing your Gmail account...")
	fmt.Println("This may take a few minutes for large accounts.\n")

	// Get user profile
	profile, err := srv.Users.GetProfile("me").Do()
	if err != nil {
		return fmt.Errorf("unable to get profile: %w", err)
	}

	fmt.Printf("Email: %s\n", profile.EmailAddress)
	fmt.Printf("Total messages: %d\n", profile.MessagesTotal)
	fmt.Printf("Total threads: %d\n", profile.ThreadsTotal)
	fmt.Printf("Storage used: %s\n\n", formatBytes(profile.MessagesTotal*1024)) // Approximate

	// Analyze emails
	stats, err := analyzeEmails(srv, limit)
	if err != nil {
		return err
	}

	printStats(stats)
	return nil
}

func analyzeEmails(srv *gmail.Service, limit int) (*EmailStats, error) {
	stats := &EmailStats{
		TopSenders:        make(map[string]int),
		CategoryBreakdown: make(map[string]int),
		LargestEmails:     make([]EmailInfo, 0),
	}

	// List messages
	pageToken := ""
	processed := 0
	batchSize := 500

	for processed < limit {
		req := srv.Users.Messages.List("me").MaxResults(int64(min(batchSize, limit-processed)))
		if pageToken != "" {
			req = req.PageToken(pageToken)
		}

		res, err := req.Do()
		if err != nil {
			return nil, fmt.Errorf("unable to list messages: %w", err)
		}

		// Process messages in parallel
		stats = processBatch(srv, res.Messages, stats)
		processed += len(res.Messages)

		fmt.Printf("\rProcessed %d emails...", processed)

		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}

	fmt.Println()
	return stats, nil
}

func processBatch(srv *gmail.Service, messages []*gmail.Message, stats *EmailStats) *EmailStats {
	var wg sync.WaitGroup
	var mu sync.Mutex

	semaphore := make(chan struct{}, 10) // Limit concurrent requests

	for _, msg := range messages {
		wg.Add(1)
		go func(msgID string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			m, err := srv.Users.Messages.Get("me", msgID).Format("metadata").Do()
			if err != nil {
				return
			}

			mu.Lock()
			defer mu.Unlock()

			stats.TotalEmails++
			stats.TotalSize += m.SizeEstimate

			// Check if unread
			for _, label := range m.LabelIds {
				if label == "UNREAD" {
					stats.UnreadEmails++
				}
				stats.CategoryBreakdown[label]++
			}

			// Get headers
			subject := ""
			from := ""
			date := ""
			for _, header := range m.Payload.Headers {
				switch header.Name {
				case "Subject":
					subject = header.Value
				case "From":
					from = header.Value
					stats.TopSenders[from]++
				case "Date":
					date = header.Value
				}
			}

			// Track large emails
			info := EmailInfo{
				ID:      m.Id,
				Subject: subject,
				From:    from,
				Date:    date,
				Size:    m.SizeEstimate,
				Labels:  m.LabelIds,
			}
			stats.LargestEmails = append(stats.LargestEmails, info)

		}(msg.Id)
	}

	wg.Wait()
	return stats
}

func printStats(stats *EmailStats) {
	fmt.Println("\n=== ANALYSIS RESULTS ===\n")
	fmt.Printf("Total emails analyzed: %d\n", stats.TotalEmails)
	fmt.Printf("Unread emails: %d (%.1f%%)\n", stats.UnreadEmails, float64(stats.UnreadEmails)/float64(stats.TotalEmails)*100)
	fmt.Printf("Total size: %s\n\n", formatBytes(stats.TotalSize))

	// Sort and show top senders
	type senderCount struct {
		Email string
		Count int
	}
	senders := make([]senderCount, 0, len(stats.TopSenders))
	for email, count := range stats.TopSenders {
		senders = append(senders, senderCount{email, count})
	}
	sort.Slice(senders, func(i, j int) bool {
		return senders[i].Count > senders[j].Count
	})

	fmt.Println("Top 10 Senders:")
	for i := 0; i < min(10, len(senders)); i++ {
		fmt.Printf("  %d. %s (%d emails)\n", i+1, senders[i].Email, senders[i].Count)
	}

	// Sort and show largest emails
	sort.Slice(stats.LargestEmails, func(i, j int) bool {
		return stats.LargestEmails[i].Size > stats.LargestEmails[j].Size
	})

	fmt.Println("\nTop 10 Largest Emails:")
	for i := 0; i < min(10, len(stats.LargestEmails)); i++ {
		e := stats.LargestEmails[i]
		fmt.Printf("  %d. %s (%s) - %s\n", i+1, formatBytes(e.Size), e.From, truncate(e.Subject, 50))
	}

	// Category breakdown
	fmt.Println("\nEmails by Category:")
	for category, count := range stats.CategoryBreakdown {
		fmt.Printf("  %s: %d\n", category, count)
	}
}

// CleanOldUnread archives or deletes old unread emails
func CleanOldUnread(client *http.Client, days int, action string, dryRun bool, skipReplied bool) error {
	ctx := context.Background()
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Gmail service: %w", err)
	}

	cutoffDate := time.Now().AddDate(0, 0, -days)
	query := fmt.Sprintf("is:unread before:%s", cutoffDate.Format("2006/01/02"))

	fmt.Printf("Searching for unread emails older than %d days...\n", days)
	messages, err := searchMessages(srv, query)
	if err != nil {
		return err
	}

	fmt.Printf("Found %d old unread emails\n", len(messages))
	if len(messages) == 0 {
		return nil
	}

	if skipReplied {
		messages, err = filterOutRepliedTo(srv, messages)
		if err != nil {
			return err
		}
		fmt.Printf("%d emails remain after excluding senders you've replied to\n", len(messages))
		if len(messages) == 0 {
			return nil
		}
	}

	if dryRun {
		fmt.Println("\nDRY RUN - No changes will be made")
		fmt.Printf("Would %s %d emails\n", action, len(messages))
		return nil
	}

	fmt.Printf("Proceeding to %s %d emails...\n", action, len(messages))
	return performBulkAction(srv, messages, action)
}

// getRepliedToAddresses returns a set of email addresses the user has sent mail to
func getRepliedToAddresses(srv *gmail.Service) (map[string]bool, error) {
	fmt.Println("Collecting addresses you've sent mail to...")
	messages, err := searchMessages(srv, "in:sent")
	if err != nil {
		return nil, err
	}

	replied := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for _, msg := range messages {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			m, err := srv.Users.Messages.Get("me", id).Format("metadata").MetadataHeaders("To").Do()
			if err != nil {
				return
			}
			for _, h := range m.Payload.Headers {
				if h.Name == "To" {
					for _, addr := range extractAddresses(h.Value) {
						mu.Lock()
						replied[addr] = true
						mu.Unlock()
					}
				}
			}
		}(msg.Id)
	}
	wg.Wait()

	fmt.Printf("Found %d unique addresses you've sent mail to\n", len(replied))
	return replied, nil
}

// filterOutRepliedTo removes messages whose sender is someone the user has replied to
func filterOutRepliedTo(srv *gmail.Service, messages []*gmail.Message) ([]*gmail.Message, error) {
	repliedTo, err := getRepliedToAddresses(srv)
	if err != nil {
		return nil, err
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	filtered := make([]*gmail.Message, 0, len(messages))

	for _, msg := range messages {
		wg.Add(1)
		go func(m *gmail.Message) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			meta, err := srv.Users.Messages.Get("me", m.Id).Format("metadata").MetadataHeaders("From").Do()
			if err != nil {
				mu.Lock()
				filtered = append(filtered, m)
				mu.Unlock()
				return
			}
			for _, h := range meta.Payload.Headers {
				if h.Name == "From" {
					for _, addr := range extractAddresses(h.Value) {
						if repliedTo[addr] {
							return // skip this message
						}
					}
				}
			}
			mu.Lock()
			filtered = append(filtered, m)
			mu.Unlock()
		}(msg)
	}
	wg.Wait()

	return filtered, nil
}

// extractAddresses parses email addresses from a header value like "Name <email>" or "email"
func extractAddresses(s string) []string {
	var addrs []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if start := strings.Index(part, "<"); start != -1 {
			if end := strings.Index(part, ">"); end > start {
				addrs = append(addrs, strings.ToLower(strings.TrimSpace(part[start+1:end])))
			}
		} else if part != "" {
			addrs = append(addrs, strings.ToLower(part))
		}
	}
	return addrs
}

// CleanPromotions cleans promotional emails
func CleanPromotions(client *http.Client, action string, days int, dryRun bool) error {
	ctx := context.Background()
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Gmail service: %w", err)
	}

	cutoffDate := time.Now().AddDate(0, 0, -days)
	query := fmt.Sprintf("category:promotions before:%s", cutoffDate.Format("2006/01/02"))

	fmt.Printf("Searching for promotional emails older than %d days...\n", days)
	messages, err := searchMessages(srv, query)
	if err != nil {
		return err
	}

	fmt.Printf("Found %d promotional emails\n", len(messages))
	if len(messages) == 0 {
		return nil
	}

	if dryRun {
		fmt.Println("\nDRY RUN - No changes will be made")
		fmt.Printf("Would %s %d emails\n", action, len(messages))
		return nil
	}

	fmt.Printf("Proceeding to %s %d emails...\n", action, len(messages))
	return performBulkAction(srv, messages, action)
}

// CleanLargeAttachments removes emails with large attachments
func CleanLargeAttachments(client *http.Client, minSize string, action string, dryRun bool) error {
	ctx := context.Background()
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Gmail service: %w", err)
	}

	query := fmt.Sprintf("has:attachment larger:%s", minSize)
	fmt.Printf("Searching for emails with attachments larger than %s...\n", minSize)

	messages, err := searchMessages(srv, query)
	if err != nil {
		return err
	}

	fmt.Printf("Found %d emails with large attachments\n", len(messages))
	if len(messages) == 0 {
		return nil
	}

	if dryRun {
		fmt.Println("\nDRY RUN - No changes will be made")
		fmt.Printf("Would %s %d emails\n", action, len(messages))
		return nil
	}

	fmt.Printf("Proceeding to %s %d emails...\n", action, len(messages))
	return performBulkAction(srv, messages, action)
}

// FindAndUnsubscribe finds emails with unsubscribe links
func FindAndUnsubscribe(client *http.Client, autoUnsubscribe bool) error {
	ctx := context.Background()
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Gmail service: %w", err)
	}

	query := "unsubscribe"
	fmt.Println("Searching for emails with unsubscribe links...")

	messages, err := searchMessages(srv, query)
	if err != nil {
		return err
	}

	fmt.Printf("Found %d emails with unsubscribe links\n", len(messages))
	if !autoUnsubscribe {
		fmt.Println("Use --auto flag to automatically attempt unsubscription")
	}

	return nil
}

// Helper functions

func searchMessages(srv *gmail.Service, query string) ([]*gmail.Message, error) {
	var allMessages []*gmail.Message
	pageToken := ""

	for {
		req := srv.Users.Messages.List("me").Q(query).MaxResults(500)
		if pageToken != "" {
			req = req.PageToken(pageToken)
		}

		res, err := req.Do()
		if err != nil {
			return nil, fmt.Errorf("unable to search messages: %w", err)
		}

		allMessages = append(allMessages, res.Messages...)

		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}

	return allMessages, nil
}

func performBulkAction(srv *gmail.Service, messages []*gmail.Message, action string) error {
	batchSize := 1000
	total := len(messages)

	for i := 0; i < total; i += batchSize {
		end := min(i+batchSize, total)
		batch := messages[i:end]

		ids := make([]string, len(batch))
		for j, msg := range batch {
			ids[j] = msg.Id
		}

		req := &gmail.BatchModifyMessagesRequest{
			Ids: ids,
		}

		switch action {
		case "archive":
			req.RemoveLabelIds = []string{"INBOX", "UNREAD"}
		case "delete":
			req.AddLabelIds = []string{"TRASH"}
		default:
			return fmt.Errorf("unknown action: %s", action)
		}

		if err := srv.Users.Messages.BatchModify("me", req).Do(); err != nil {
			return fmt.Errorf("batch modify failed: %w", err)
		}

		fmt.Printf("\rProcessed %d/%d emails...", end, total)
	}

	fmt.Println("\nDone!")
	return nil
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
			num, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, err
			}
			return int64(num * float64(mult)), nil
		}
	}

	return 0, fmt.Errorf("invalid size format: %s", sizeStr)
}
