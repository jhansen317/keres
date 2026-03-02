package gmail

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
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
	rl := newAPILimiter()

	fmt.Println("Analyzing your Gmail account...")
	fmt.Println("This may take a few minutes for large accounts.\n")

	// Get user profile
	var profile *gmail.Profile
	if err := rl.do(ctx, func() error {
		var err error
		profile, err = srv.Users.GetProfile("me").Do()
		return err
	}); err != nil {
		return fmt.Errorf("unable to get profile: %w", err)
	}

	fmt.Printf("Email: %s\n", profile.EmailAddress)
	fmt.Printf("Total messages: %d\n", profile.MessagesTotal)
	fmt.Printf("Total threads: %d\n", profile.ThreadsTotal)
	fmt.Printf("Storage used: %s\n\n", formatBytes(profile.MessagesTotal*1024)) // Approximate

	// Analyze emails
	stats, err := analyzeEmails(ctx, srv, rl, limit)
	if err != nil {
		return err
	}

	printStats(stats)
	return nil
}

func analyzeEmails(ctx context.Context, srv *gmail.Service, rl *apiLimiter, limit int) (*EmailStats, error) {
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

		var res *gmail.ListMessagesResponse
		if err := rl.do(ctx, func() error {
			var err error
			res, err = req.Do()
			return err
		}); err != nil {
			return nil, fmt.Errorf("unable to list messages: %w", err)
		}

		// Process messages in parallel
		stats = processBatch(ctx, srv, rl, res.Messages, stats)
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

func processBatch(ctx context.Context, srv *gmail.Service, rl *apiLimiter, messages []*gmail.Message, stats *EmailStats) *EmailStats {
	var wg sync.WaitGroup
	var mu sync.Mutex

	semaphore := make(chan struct{}, 10) // Limit concurrent requests

	for _, msg := range messages {
		wg.Add(1)
		go func(msgID string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			var m *gmail.Message
			if err := rl.do(ctx, func() error {
				var err error
				m, err = srv.Users.Messages.Get("me", msgID).Format("metadata").Do()
				return err
			}); err != nil {
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
	rl := newAPILimiter()

	cutoffDate := time.Now().AddDate(0, 0, -days)
	query := fmt.Sprintf("is:unread before:%s", cutoffDate.Format("2006/01/02"))

	fmt.Printf("Searching for unread emails older than %d days...\n", days)
	messages, err := searchMessages(ctx, srv, rl, query)
	if err != nil {
		return err
	}

	fmt.Printf("Found %d old unread emails\n", len(messages))
	if len(messages) == 0 {
		return nil
	}

	if skipReplied {
		messages, err = filterOutRepliedTo(ctx, srv, rl, messages)
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
	return performBulkAction(ctx, srv, rl, messages, action)
}

// getRepliedToAddresses returns a set of email addresses the user has sent mail to
func getRepliedToAddresses(ctx context.Context, srv *gmail.Service, rl *apiLimiter) (map[string]bool, error) {
	fmt.Println("Collecting addresses you've sent mail to...")
	messages, err := searchMessages(ctx, srv, rl, "in:sent")
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

			var m *gmail.Message
			if err := rl.do(ctx, func() error {
				var err error
				m, err = srv.Users.Messages.Get("me", id).Format("metadata").MetadataHeaders("To").Do()
				return err
			}); err != nil {
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
func filterOutRepliedTo(ctx context.Context, srv *gmail.Service, rl *apiLimiter, messages []*gmail.Message) ([]*gmail.Message, error) {
	repliedTo, err := getRepliedToAddresses(ctx, srv, rl)
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

			var meta *gmail.Message
			if err := rl.do(ctx, func() error {
				var err error
				meta, err = srv.Users.Messages.Get("me", m.Id).Format("metadata").MetadataHeaders("From").Do()
				return err
			}); err != nil {
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

// CleanCategory cleans emails from a Gmail category (promotions, updates, social, forums).
// exclude is a comma-separated list of email addresses or domains to skip.
func CleanCategory(client *http.Client, category string, action string, days int, dryRun bool, exclude string) error {
	ctx := context.Background()
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Gmail service: %w", err)
	}
	rl := newAPILimiter()

	cutoffDate := time.Now().AddDate(0, 0, -days)
	query := fmt.Sprintf("category:%s before:%s", category, cutoffDate.Format("2006/01/02"))

	fmt.Printf("Searching for %s emails older than %d days...\n", category, days)
	messages, err := searchMessages(ctx, srv, rl, query)
	if err != nil {
		return err
	}

	fmt.Printf("Found %d %s emails\n", len(messages), category)
	if len(messages) == 0 {
		return nil
	}

	// Filter out excluded senders
	if exclude != "" {
		excluded := parseExcludeList(exclude)
		fmt.Println("Filtering out excluded senders...")
		messages, err = filterExcludedSenders(ctx, srv, rl, messages, excluded)
		if err != nil {
			return err
		}
		fmt.Printf("%d emails remain after exclusions\n", len(messages))
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
	return performBulkAction(ctx, srv, rl, messages, action)
}

// CleanLargeAttachments removes emails with large attachments
func CleanLargeAttachments(client *http.Client, minSize string, action string, dryRun bool) error {
	ctx := context.Background()
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Gmail service: %w", err)
	}
	rl := newAPILimiter()

	query := fmt.Sprintf("has:attachment larger:%s", minSize)
	fmt.Printf("Searching for emails with attachments larger than %s...\n", minSize)

	messages, err := searchMessages(ctx, srv, rl, query)
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
	return performBulkAction(ctx, srv, rl, messages, action)
}

// unsubscribeInfo holds parsed unsubscribe data for a sender
type unsubscribeInfo struct {
	From string
	URLs []string // HTTPS unsubscribe URLs
}

// FindAndUnsubscribe finds emails with unsubscribe links and optionally unsubscribes.
// exclude is a comma-separated list of email addresses or domains to skip.
func FindAndUnsubscribe(client *http.Client, autoUnsubscribe bool, exclude string) error {
	ctx := context.Background()
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Gmail service: %w", err)
	}
	rl := newAPILimiter()

	query := "unsubscribe"
	fmt.Println("Searching for emails with unsubscribe links...")

	messages, err := searchMessages(ctx, srv, rl, query)
	if err != nil {
		return err
	}

	fmt.Printf("Found %d emails mentioning unsubscribe\n", len(messages))
	if len(messages) == 0 {
		return nil
	}

	// Phase 1: Fetch List-Unsubscribe headers in parallel, deduplicate by sender
	fmt.Println("Extracting unsubscribe links from headers...")
	senderURLs := make(map[string][]string) // sender -> HTTPS URLs
	mailtoOnly := make(map[string]bool)     // senders with only mailto links
	noHeader := make(map[string]string)     // sender -> message ID (for body fallback)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for _, msg := range messages {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var m *gmail.Message
			if err := rl.do(ctx, func() error {
				var err error
				m, err = srv.Users.Messages.Get("me", id).
					Format("metadata").
					MetadataHeaders("List-Unsubscribe").
					MetadataHeaders("From").
					Do()
				return err
			}); err != nil {
				return
			}

			from := ""
			unsubHeader := ""
			for _, h := range m.Payload.Headers {
				switch h.Name {
				case "From":
					from = h.Value
				case "List-Unsubscribe":
					unsubHeader = h.Value
				}
			}

			if from == "" {
				return
			}

			addrs := extractAddresses(from)
			if len(addrs) == 0 {
				return
			}
			senderKey := addrs[0]

			mu.Lock()
			defer mu.Unlock()

			// Skip if we already have this sender
			if _, seen := senderURLs[senderKey]; seen {
				return
			}
			if mailtoOnly[senderKey] || noHeader[senderKey] != "" {
				return
			}

			if unsubHeader == "" {
				noHeader[senderKey] = id
				return
			}

			httpsURLs := extractUnsubscribeURLs(unsubHeader)
			if len(httpsURLs) > 0 {
				senderURLs[senderKey] = httpsURLs
			} else {
				mailtoOnly[senderKey] = true
			}
		}(msg.Id)
	}
	wg.Wait()

	fmt.Printf("  Found %d senders via List-Unsubscribe header\n", len(senderURLs))

	// Phase 2: For senders without header, fetch one message body and extract links
	if len(noHeader) > 0 {
		fmt.Printf("  Scanning email body for %d senders without header...\n", len(noHeader))
		var wg2 sync.WaitGroup

		for sender, msgID := range noHeader {
			wg2.Add(1)
			go func(sender, id string) {
				defer wg2.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				var m *gmail.Message
				if err := rl.do(ctx, func() error {
					var err error
					m, err = srv.Users.Messages.Get("me", id).Format("full").Do()
					return err
				}); err != nil {
					return
				}

				body := extractMessageBody(m.Payload)
				urls := extractBodyUnsubscribeURLs(body)

				if len(urls) > 0 {
					mu.Lock()
					senderURLs[sender] = urls
					mu.Unlock()
				}
			}(sender, msgID)
		}
		wg2.Wait()
		fmt.Printf("  Found %d additional senders via email body\n", len(senderURLs)-len(mailtoOnly))
	}

	// Apply exclusions
	if exclude != "" {
		excluded := parseExcludeList(exclude)
		for sender := range senderURLs {
			if isExcluded(sender, excluded) {
				delete(senderURLs, sender)
			}
		}
		for sender := range mailtoOnly {
			if isExcluded(sender, excluded) {
				delete(mailtoOnly, sender)
			}
		}
	}

	fmt.Printf("\nFound %d senders with HTTPS unsubscribe links\n", len(senderURLs))
	if len(mailtoOnly) > 0 {
		fmt.Printf("Skipping %d senders with mailto-only unsubscribe (not supported)\n", len(mailtoOnly))
	}

	if len(senderURLs) == 0 {
		fmt.Println("No HTTPS unsubscribe links found.")
		return nil
	}

	// List senders
	i := 0
	for sender := range senderURLs {
		i++
		fmt.Printf("  %d. %s\n", i, sender)
	}

	if !autoUnsubscribe {
		fmt.Println("\nUse --auto flag to automatically unsubscribe from these senders")
		return nil
	}

	// Perform unsubscribes
	fmt.Printf("\nUnsubscribing from %d senders...\n", len(senderURLs))
	httpClient := &http.Client{Timeout: 10 * time.Second}
	succeeded := 0
	failed := 0
	unsubSem := make(chan struct{}, 3) // lower concurrency for external requests
	var unsubWg sync.WaitGroup

	for sender, urls := range senderURLs {
		unsubWg.Add(1)
		go func(sender string, urls []string) {
			defer unsubWg.Done()
			unsubSem <- struct{}{}
			defer func() { <-unsubSem }()

			var lastErr error
			for _, u := range urls {
				if err := unsubscribeHTTPS(u, httpClient); err != nil {
					lastErr = err
					continue
				}
				lastErr = nil
				break
			}

			mu.Lock()
			defer mu.Unlock()
			if lastErr != nil {
				failed++
				fmt.Printf("  FAIL: %s (%v)\n", sender, lastErr)
			} else {
				succeeded++
				fmt.Printf("  OK:   %s\n", sender)
			}
		}(sender, urls)
	}
	unsubWg.Wait()

	fmt.Printf("\nDone! Succeeded: %d, Failed: %d\n", succeeded, failed)
	return nil
}

// extractUnsubscribeURLs parses a List-Unsubscribe header and returns HTTPS URLs.
// Header format: <https://example.com/unsub>, <mailto:unsub@example.com>
func extractUnsubscribeURLs(header string) []string {
	var urls []string
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		// Strip angle brackets
		part = strings.TrimPrefix(part, "<")
		part = strings.TrimSuffix(part, ">")
		part = strings.TrimSpace(part)

		parsed, err := url.Parse(part)
		if err != nil {
			continue
		}
		if parsed.Scheme == "https" || parsed.Scheme == "http" {
			urls = append(urls, part)
		}
	}
	return urls
}

// extractMessageBody recursively extracts the text/html (preferred) or text/plain body from a message payload.
func extractMessageBody(payload *gmail.MessagePart) string {
	if payload == nil {
		return ""
	}

	// If this part has a body with data, check mime type
	if payload.Body != nil && payload.Body.Data != "" {
		if payload.MimeType == "text/html" || payload.MimeType == "text/plain" {
			// Gmail returns base64url-encoded data
			decoded, err := base64Decode(payload.Body.Data)
			if err == nil {
				return decoded
			}
		}
	}

	// Recurse into parts, prefer HTML
	htmlBody := ""
	plainBody := ""
	for _, part := range payload.Parts {
		result := extractMessageBody(part)
		if result != "" {
			if part.MimeType == "text/html" {
				htmlBody = result
			} else if plainBody == "" {
				plainBody = result
			}
		}
	}
	if htmlBody != "" {
		return htmlBody
	}
	return plainBody
}

// base64Decode decodes Gmail's base64url-encoded body data.
func base64Decode(data string) (string, error) {
	decoded, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(data)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// extractBodyUnsubscribeURLs finds unsubscribe-related HTTPS URLs in an email body.
func extractBodyUnsubscribeURLs(body string) []string {
	if body == "" {
		return nil
	}

	body = strings.ToLower(body)
	var urls []string
	seen := make(map[string]bool)

	// Find href="..." links near "unsubscribe" text
	// Look for href attributes containing "unsubscribe" in the URL
	idx := 0
	for idx < len(body) {
		hrefPos := strings.Index(body[idx:], "href=\"")
		if hrefPos == -1 {
			break
		}
		hrefPos += idx + 6 // skip past href="
		endPos := strings.Index(body[hrefPos:], "\"")
		if endPos == -1 {
			break
		}

		link := body[hrefPos : hrefPos+endPos]
		idx = hrefPos + endPos

		if !strings.HasPrefix(link, "http") {
			continue
		}
		if !strings.Contains(link, "unsubscribe") && !strings.Contains(link, "unsub") && !strings.Contains(link, "opt-out") && !strings.Contains(link, "optout") {
			continue
		}
		if !seen[link] {
			seen[link] = true
			urls = append(urls, link)
		}
	}

	return urls
}

// unsubscribeHTTPS sends an unsubscribe request to an HTTPS URL.
// Tries POST first (RFC 8058 one-click), falls back to GET.
func unsubscribeHTTPS(unsubURL string, httpClient *http.Client) error {
	// Try POST with RFC 8058 one-click body
	resp, err := httpClient.Post(unsubURL, "application/x-www-form-urlencoded",
		strings.NewReader("List-Unsubscribe=One-Click"))
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return nil
		}
	}

	// Fall back to GET
	resp, err = httpClient.Get(unsubURL)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// filterExcludedSenders removes messages from senders matching the exclude list.
func filterExcludedSenders(ctx context.Context, srv *gmail.Service, rl *apiLimiter, messages []*gmail.Message, excluded []string) ([]*gmail.Message, error) {
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

			var meta *gmail.Message
			if err := rl.do(ctx, func() error {
				var err error
				meta, err = srv.Users.Messages.Get("me", m.Id).
					Format("metadata").MetadataHeaders("From").Do()
				return err
			}); err != nil {
				mu.Lock()
				filtered = append(filtered, m) // keep on error (safe default)
				mu.Unlock()
				return
			}

			for _, h := range meta.Payload.Headers {
				if h.Name == "From" {
					for _, addr := range extractAddresses(h.Value) {
						if isExcluded(addr, excluded) {
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

// parseExcludeList splits a comma-separated exclude string into lowercase entries.
func parseExcludeList(exclude string) []string {
	var entries []string
	for _, e := range strings.Split(exclude, ",") {
		e = strings.TrimSpace(strings.ToLower(e))
		if e != "" {
			entries = append(entries, e)
		}
	}
	return entries
}

// isExcluded checks if a sender email matches any exclude entry.
// Entries can be full emails (user@example.com) or domains (example.com).
func isExcluded(sender string, excluded []string) bool {
	sender = strings.ToLower(sender)
	for _, e := range excluded {
		if sender == e {
			return true
		}
		if strings.HasSuffix(sender, "@"+e) {
			return true
		}
	}
	return false
}

// Helper functions

func searchMessages(ctx context.Context, srv *gmail.Service, rl *apiLimiter, query string) ([]*gmail.Message, error) {
	var allMessages []*gmail.Message
	pageToken := ""

	for {
		req := srv.Users.Messages.List("me").Q(query).MaxResults(500)
		if pageToken != "" {
			req = req.PageToken(pageToken)
		}

		var res *gmail.ListMessagesResponse
		if err := rl.do(ctx, func() error {
			var err error
			res, err = req.Do()
			return err
		}); err != nil {
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

func performBulkAction(ctx context.Context, srv *gmail.Service, rl *apiLimiter, messages []*gmail.Message, action string) error {
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

		if err := rl.do(ctx, func() error {
			return srv.Users.Messages.BatchModify("me", req).Do()
		}); err != nil {
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
