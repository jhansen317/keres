package cmd

import (
	"fmt"

	"github.com/jhansen317/keres/internal/auth"
	"github.com/jhansen317/keres/internal/gmail"
	"github.com/spf13/cobra"
)

var gmailCmd = &cobra.Command{
	Use:   "gmail",
	Short: "Gmail cleanup and organization tools",
	Long:  `Analyze and clean up your Gmail account to reclaim storage space.`,
}

var gmailAnalyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyze Gmail storage usage",
	Long: `Analyze your Gmail account to identify:
- Largest emails
- Emails with large attachments
- Most common senders
- Old unread emails
- Storage usage by label/category`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := auth.GetGoogleClient([]string{"https://www.googleapis.com/auth/gmail.readonly"})
		if err != nil {
			return fmt.Errorf("authentication required: run 'keres auth login'")
		}

		limit, _ := cmd.Flags().GetInt("limit")
		return gmail.Analyze(client, limit)
	},
}

var gmailCleanOldCmd = &cobra.Command{
	Use:   "clean-old",
	Short: "Archive or delete old unread emails",
	Long: `Clean up old unread emails that are likely no longer relevant.
You can choose to archive or delete emails older than a specified number of days.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := auth.GetGoogleClient([]string{"https://www.googleapis.com/auth/gmail.modify"})
		if err != nil {
			return fmt.Errorf("authentication required: run 'keres auth login'")
		}

		days, _ := cmd.Flags().GetInt("days")
		action, _ := cmd.Flags().GetString("action")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		skipReplied, _ := cmd.Flags().GetBool("skip-replied")

		return gmail.CleanOldUnread(client, days, action, dryRun, skipReplied)
	},
}

var gmailCleanPromotionsCmd = &cobra.Command{
	Use:   "clean-promotions",
	Short: "Clean promotional emails",
	Long:  `Bulk archive or delete emails in the Promotions category.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := auth.GetGoogleClient([]string{"https://www.googleapis.com/auth/gmail.modify"})
		if err != nil {
			return fmt.Errorf("authentication required: run 'keres auth login'")
		}

		action, _ := cmd.Flags().GetString("action")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		days, _ := cmd.Flags().GetInt("older-than")

		return gmail.CleanPromotions(client, action, days, dryRun)
	},
}

var gmailCleanLargeCmd = &cobra.Command{
	Use:   "clean-large",
	Short: "Remove emails with large attachments",
	Long:  `Find and remove emails with attachments larger than a specified size.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := auth.GetGoogleClient([]string{"https://www.googleapis.com/auth/gmail.modify"})
		if err != nil {
			return fmt.Errorf("authentication required: run 'keres auth login'")
		}

		minSize, _ := cmd.Flags().GetString("min-size")
		action, _ := cmd.Flags().GetString("action")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		return gmail.CleanLargeAttachments(client, minSize, action, dryRun)
	},
}

var gmailUnsubscribeCmd = &cobra.Command{
	Use:   "unsubscribe",
	Short: "Find and unsubscribe from mailing lists",
	Long:  `Identify emails with unsubscribe links and optionally unsubscribe automatically.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := auth.GetGoogleClient([]string{"https://www.googleapis.com/auth/gmail.modify"})
		if err != nil {
			return fmt.Errorf("authentication required: run 'keres auth login'")
		}

		autoUnsubscribe, _ := cmd.Flags().GetBool("auto")
		return gmail.FindAndUnsubscribe(client, autoUnsubscribe)
	},
}

func init() {
	rootCmd.AddCommand(gmailCmd)
	gmailCmd.AddCommand(gmailAnalyzeCmd)
	gmailCmd.AddCommand(gmailCleanOldCmd)
	gmailCmd.AddCommand(gmailCleanPromotionsCmd)
	gmailCmd.AddCommand(gmailCleanLargeCmd)
	gmailCmd.AddCommand(gmailUnsubscribeCmd)

	// Analyze flags
	gmailAnalyzeCmd.Flags().Int("limit", 1000, "Number of emails to analyze")

	// Clean old flags
	gmailCleanOldCmd.Flags().Int("days", 365, "Clean emails older than this many days")
	gmailCleanOldCmd.Flags().String("action", "archive", "Action to take: archive or delete")
	gmailCleanOldCmd.Flags().Bool("dry-run", true, "Preview changes without applying them")
	gmailCleanOldCmd.Flags().Bool("skip-replied", false, "Skip emails from senders you have ever replied to")

	// Clean promotions flags
	gmailCleanPromotionsCmd.Flags().String("action", "archive", "Action to take: archive or delete")
	gmailCleanPromotionsCmd.Flags().Bool("dry-run", true, "Preview changes without applying them")
	gmailCleanPromotionsCmd.Flags().Int("older-than", 30, "Only clean promotions older than this many days")

	// Clean large flags
	gmailCleanLargeCmd.Flags().String("min-size", "10MB", "Minimum attachment size (e.g., 5MB, 10MB, 100MB)")
	gmailCleanLargeCmd.Flags().String("action", "archive", "Action to take: archive or delete")
	gmailCleanLargeCmd.Flags().Bool("dry-run", true, "Preview changes without applying them")

	// Unsubscribe flags
	gmailUnsubscribeCmd.Flags().Bool("auto", false, "Automatically unsubscribe (use with caution)")
}
