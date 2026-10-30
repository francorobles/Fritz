// Command outlookauth does Fritz's one-time company Outlook
// sign-in: Microsoft's device-code flow. It prints a short code;
// you open the Microsoft page on any device, enter the code, and
// sign in as yourself — no IT admin ticket, and the token can
// only read your mailbox (Mail.Read). The token is saved at mode
// 0600 next to the other integration files.
//
// The client_id comes from an Azure AD app registration (public
// client, "allow public client flows" on). Put it in
// integrations.json under outlook, or pass -client.
//
//	go run ./cmd/outlookauth [-tenant common] [-client <id>]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/francorobles/fritz/api/internal/integrations"
)

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Message         string `json:"message"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

type tokenError struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

func main() {
	tenantFlag := flag.String("tenant", "", "Azure tenant (default: from config, else common)")
	clientFlag := flag.String("client", "", "Azure app client_id (default: from config)")
	flag.Parse()

	cfg, _, err := integrations.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tenant := *tenantFlag
	if tenant == "" {
		tenant = cfg.Outlook.TenantOrDefault()
	}
	clientID := *clientFlag
	if clientID == "" {
		clientID = cfg.Outlook.ClientID
	}
	if clientID == "" {
		fmt.Fprintln(os.Stderr, "no client_id: register an Azure AD app (public client) and set outlook.client_id in integrations.json, or pass -client")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	base := "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0"

	dc, err := startDeviceCode(ctx, base, clientID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "device code:", err)
		os.Exit(1)
	}
	fmt.Println(dc.Message)
	fmt.Printf("Or open %s and enter code %s\n", dc.VerificationURI, dc.UserCode)

	tok, err := pollToken(ctx, base, clientID, dc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sign-in:", err)
		os.Exit(1)
	}
	path := integrations.OutlookTokenFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "save token:", err)
		os.Exit(1)
	}
	data, err := json.Marshal(tok)
	if err != nil {
		fmt.Fprintln(os.Stderr, "save token:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "save token:", err)
		os.Exit(1)
	}
	fmt.Printf("Saved %s (mode 0600), scope Mail.Read. Fritz can now read your Outlook mail.\n", path)
}

func startDeviceCode(ctx context.Context, base, clientID string) (deviceCodeResponse, error) {
	form := url.Values{
		"client_id": {clientID},
		"scope":     {strings.Join(integrations.OutlookScopes, " ")},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/devicecode", strings.NewReader(form.Encode()))
	if err != nil {
		return deviceCodeResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return deviceCodeResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return deviceCodeResponse{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var dc deviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return deviceCodeResponse{}, err
	}
	return dc, nil
}

func pollToken(ctx context.Context, base, clientID string, dc deviceCodeResponse) (*oauth2.Token, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("code expired — run outlookauth again")
		}
		form := url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"client_id":   {clientID},
			"device_code": {dc.DeviceCode},
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/token", strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var tok oauth2.Token
			if err := json.Unmarshal(body, &tok); err != nil {
				return nil, err
			}
			return &tok, nil
		}
		var terr tokenError
		if err := json.Unmarshal(body, &terr); err != nil {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		switch terr.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		default:
			return nil, fmt.Errorf("%s: %s", terr.Error, terr.Description)
		}
	}
}
