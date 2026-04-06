package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

type credentials struct {
	Token string `json:"token"`
	Email string `json:"email"`
}

func credentialsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "inferall", "credentials.json")
}

func loadCredentials() (*credentials, error) {
	data, err := os.ReadFile(credentialsPath())
	if err != nil {
		return nil, err
	}
	var creds credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

func saveCredentials(creds *credentials) error {
	dir := filepath.Dir(credentialsPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(creds, "", "  ")
	return os.WriteFile(credentialsPath(), data, 0600)
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return fmt.Errorf("unsupported platform")
	}
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage InferAll authentication",
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in to InferAll",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Find an available port
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("failed to start local server: %w", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		listener.Close()

		callbackURL := fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port)
		state := fmt.Sprintf("%d", time.Now().UnixNano())

		authURL := fmt.Sprintf(
			"https://inferall.ai/auth/extension?callback=%s&state=%s",
			callbackURL,
			state,
		)

		fmt.Println("Opening browser to sign in...")
		fmt.Printf("If the browser doesn't open, visit:\n%s\n\n", authURL)

		if err := openBrowser(authURL); err != nil {
			fmt.Printf("Could not open browser: %v\n", err)
		}

		// Start local HTTP server to receive the callback
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		var creds *credentials
		mux := http.NewServeMux()
		mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
			token := r.URL.Query().Get("token")
			email := r.URL.Query().Get("email")
			returnedState := r.URL.Query().Get("state")

			if returnedState != state {
				http.Error(w, "State mismatch", http.StatusBadRequest)
				return
			}

			if token == "" {
				http.Error(w, "Missing token", http.StatusBadRequest)
				return
			}

			creds = &credentials{Token: token, Email: email}

			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><body style="font-family:system-ui;text-align:center;padding:60px">
				<h2>Signed in to InferAll</h2>
				<p>You can close this tab and return to your terminal.</p>
			</body></html>`)

			cancel()
		})

		server := &http.Server{
			Addr:    fmt.Sprintf("127.0.0.1:%d", port),
			Handler: mux,
		}

		go func() {
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
				cancel()
			}
		}()

		fmt.Println("Waiting for sign-in...")
		<-ctx.Done()

		server.Shutdown(context.Background())

		if creds == nil {
			return fmt.Errorf("sign-in timed out or was cancelled")
		}

		if err := saveCredentials(creds); err != nil {
			return fmt.Errorf("failed to save credentials: %w", err)
		}

		// Set ANTHROPIC_API_KEY env var for the current process
		os.Setenv("ANTHROPIC_API_KEY", creds.Token)

		fmt.Printf("\nSigned in as %s\n", creds.Email)
		fmt.Printf("Credentials saved to %s\n", credentialsPath())
		return nil
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Sign out of InferAll",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := credentialsPath()
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove credentials: %w", err)
		}
		fmt.Println("Signed out of InferAll")
		return nil
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := loadCredentials()
		if err != nil {
			fmt.Println("Not signed in. Run 'inferall auth login' to sign in.")
			return nil
		}
		fmt.Printf("Signed in as: %s\n", creds.Email)
		fmt.Printf("API key: %s...%s\n", creds.Token[:12], creds.Token[len(creds.Token)-4:])
		return nil
	},
}

func init() {
	authCmd.AddCommand(loginCmd)
	authCmd.AddCommand(logoutCmd)
	authCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(authCmd)
}
