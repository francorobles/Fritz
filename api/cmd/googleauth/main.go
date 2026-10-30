// Command googleauth does Fritz's one-time Google sign-in: it
// runs the OAuth loopback flow against the Desktop client in
// google-client.json and saves the token (mode 0600) where the
// Gmail client expects it. Run it on a machine with a browser —
// on the Pi, auth on your laptop and copy google-token.json over.
//
// Setup first (fritz-integrations skill): Google Cloud project,
// Gmail + Calendar APIs enabled, OAuth Desktop client JSON saved
// to ~/.config/fritz/google-client.json (FRITZ_CONFIG_DIR).
//
//	go run ./cmd/googleauth
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/francorobles/fritz/api/internal/integrations"
)

func main() {
	clientJSON, err := os.ReadFile(integrations.GoogleClientFile())
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", integrations.GoogleClientFile(), err)
		fmt.Fprintln(os.Stderr, "Create an OAuth Desktop client in Google Cloud and save the JSON there first (see the fritz-integrations skill).")
		os.Exit(1)
	}
	config, err := google.ConfigFromJSON(clientJSON, integrations.GoogleScopes...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "client config:", err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	defer listener.Close()
	config.RedirectURL = fmt.Sprintf("http://%s/", listener.Addr().String())

	stateBytes := make([]byte, 16)
	_, _ = rand.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	url := config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	fmt.Println("Open this URL in your browser and approve Fritz:")
	fmt.Println(url)
	openBrowser(url)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- fmt.Errorf("oauth state mismatch — try again")
			return
		}
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			http.Error(w, errMsg, http.StatusBadRequest)
			errCh <- fmt.Errorf("google said %s", errMsg)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "no code", http.StatusBadRequest)
			errCh <- fmt.Errorf("no code in callback")
			return
		}
		fmt.Fprintln(w, "Fritz is connected. You can close this tab and return to the terminal.")
		codeCh <- code
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		fmt.Fprintln(os.Stderr, "auth:", err)
		os.Exit(1)
	}

	token, err := config.Exchange(context.Background(), code)
	if err != nil {
		fmt.Fprintln(os.Stderr, "exchange:", err)
		os.Exit(1)
	}
	path := integrations.GoogleTokenFile()
	if err := saveToken(path, token); err != nil {
		fmt.Fprintln(os.Stderr, "save token:", err)
		os.Exit(1)
	}
	fmt.Printf("Saved %s (mode 0600). Scopes: gmail.readonly, gmail.compose, calendar.events.\n", path)
	fmt.Println("On the Pi: copy this file to the same path in FRITZ_CONFIG_DIR there.")
}

// saveToken mirrors the integrations package's token writer
// (unexported there; this is the one place outside it that
// writes tokens).
func saveToken(path string, token *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start() // best effort; the printed URL is the fallback
}
