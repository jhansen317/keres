package cmd

import (
	"fmt"

	"github.com/jhansen317/keres/internal/auth"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication for Google services",
	Long:  `Authenticate with Google services (Gmail, Drive) and cache credentials for future use.`,
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with Google",
	Long: `Start the OAuth2 flow to authenticate with Google services.
This will open a browser window for you to grant permissions.
Credentials will be cached locally for future use.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scopes, _ := cmd.Flags().GetStringSlice("scopes")
		if len(scopes) == 0 {
			scopes = []string{
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/gmail.modify",
				"https://www.googleapis.com/auth/drive",
			}
		}

		// Clear cached token so we always prompt for fresh credentials
		_ = auth.Logout()

		client, err := auth.GetGoogleClient(scopes)
		if err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}

		fmt.Println("Authenticated with Google.")
		fmt.Println("Credentials have been cached for future use.")
		_ = client // client is ready to use
		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check authentication status",
	RunE: func(cmd *cobra.Command, args []string) error {
		hasToken, email, err := auth.CheckAuthStatus()
		if err != nil {
			return err
		}

		if hasToken {
			fmt.Printf("Authenticated as: %s\n", email)
		} else {
			fmt.Println("Not authenticated. Run 'keres auth login' to authenticate.")
		}
		return nil
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove cached credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := auth.Logout(); err != nil {
			return err
		}
		fmt.Println("Logged out. Credentials removed.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authLogoutCmd)

	authLoginCmd.Flags().StringSlice("scopes", nil, "Custom OAuth2 scopes (optional)")
}
