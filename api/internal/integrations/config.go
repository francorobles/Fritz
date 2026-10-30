// Package integrations connects Fritz to the user's accounts:
// Gmail, AWS WorkMail, company Outlook, Jira, and WhatsApp.
// See the fritz-integrations skill.
//
// Settings live in integrations.json inside FRITZ_CONFIG_DIR
// (default ~/.config/fritz) — never in the repo. The file holds
// only non-secret settings; secrets are always referenced by the
// NAME of an environment variable, and OAuth token files live
// next to it at mode 0600. Every provider is optional and
// disabled unless its block says "enabled": true.
package integrations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config is integrations.json.
type Config struct {
	Gmail    GmailConfig    `json:"gmail"`
	Calendar CalendarConfig `json:"calendar"`
	WorkMail WorkMailConfig `json:"workmail"`
	Outlook  OutlookConfig  `json:"outlook"`
	Jira     JiraConfig     `json:"jira"`
	WhatsApp WhatsAppConfig `json:"whatsapp"`
}

// CalendarConfig is Google Calendar. It shares Gmail's OAuth
// token (googleauth already consents to calendar.events).
type CalendarConfig struct {
	Enabled bool `json:"enabled"`
}

// GmailConfig needs no secrets: the OAuth client and token are
// files in the config dir (google-client.json, google-token.json).
type GmailConfig struct {
	Enabled bool `json:"enabled"`
}

// WorkMailConfig is IMAP over TLS (WorkMail has no Gmail-style
// API). Host derives from Region unless set explicitly.
type WorkMailConfig struct {
	Enabled     bool   `json:"enabled"`
	Address     string `json:"address"`      // mailbox address, also the IMAP user
	PasswordEnv string `json:"password_env"` // env var holding the mailbox password
	Region      string `json:"region"`       // default us-east-1
	Host        string `json:"host"`         // override: imap.mail.<region>.awsapps.com
	Port        int    `json:"port"`         // default 993
}

// HostOrDefault resolves the IMAP host.
func (c WorkMailConfig) HostOrDefault() string {
	if c.Host != "" {
		return c.Host
	}
	region := c.Region
	if region == "" {
		region = "us-east-1"
	}
	return "imap.mail." + region + ".awsapps.com"
}

// Port resolves the IMAP port.
func (c WorkMailConfig) PortOrDefault() int {
	if c.Port > 0 {
		return c.Port
	}
	return 993
}

// OutlookConfig is Microsoft Graph with delegated device-code
// sign-in: ClientID is your Azure app registration (public, not
// a secret); tokens land in outlook-token.json.
type OutlookConfig struct {
	Enabled  bool   `json:"enabled"`
	ClientID string `json:"client_id"`
	Tenant   string `json:"tenant"` // default "common"
}

// TenantOrDefault resolves the tenant.
func (c OutlookConfig) TenantOrDefault() string {
	if c.Tenant != "" {
		return c.Tenant
	}
	return "common"
}

// JiraConfig covers Jira Cloud (email + API token, HTTP Basic)
// and Jira Server/DC (personal access token, Bearer when no
// email is configured).
type JiraConfig struct {
	Enabled  bool   `json:"enabled"`
	BaseURL  string `json:"base_url"` // https://company.atlassian.net
	EmailEnv string `json:"email_env"`
	TokenEnv string `json:"token_env"`
}

// WhatsAppConfig is the WhatsApp Business Cloud API, both ways:
// outbound alerts (templates) and inbound chat (webhook).
// Allow lists the owner's numbers (digits, with country code);
// messages from anyone else are ignored.
type WhatsAppConfig struct {
	Enabled        bool     `json:"enabled"`
	PhoneNumberID  string   `json:"phone_number_id"`
	TokenEnv       string   `json:"token_env"`        // permanent/system-user token
	AppSecretEnv   string   `json:"app_secret_env"`   // webhook signature check
	VerifyTokenEnv string   `json:"verify_token_env"` // webhook subscription challenge
	Allow          []string `json:"allow"`
	AlertTemplate  string   `json:"alert_template"` // default hello_world (Meta test template)
	GraphVersion   string   `json:"graph_version"`  // default v21.0
}

// AlertTemplateOrDefault resolves the alert template name.
func (c WhatsAppConfig) AlertTemplateOrDefault() string {
	if c.AlertTemplate != "" {
		return c.AlertTemplate
	}
	return "hello_world"
}

// GraphVersionOrDefault resolves the Graph API version.
func (c WhatsAppConfig) GraphVersionOrDefault() string {
	if c.GraphVersion != "" {
		return c.GraphVersion
	}
	return "v21.0"
}

// ConfigDir is where Fritz keeps integration files:
// $FRITZ_CONFIG_DIR or ~/.config/fritz.
func ConfigDir() string {
	if dir := os.Getenv("FRITZ_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config/fritz"
	}
	return filepath.Join(home, ".config", "fritz")
}

// File paths inside the config dir.
func ConfigFile() string       { return filepath.Join(ConfigDir(), "integrations.json") }
func GoogleClientFile() string { return filepath.Join(ConfigDir(), "google-client.json") }
func GoogleTokenFile() string  { return filepath.Join(ConfigDir(), "google-token.json") }
func OutlookTokenFile() string { return filepath.Join(ConfigDir(), "outlook-token.json") }

// Load reads the integrations config. A missing file is not an
// error: it means a fresh install with everything disabled.
// FRITZ_INTEGRATIONS_FILE overrides the full path (tests).
func Load() (Config, string, error) {
	path := os.Getenv("FRITZ_INTEGRATIONS_FILE")
	if path == "" {
		path = ConfigFile()
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Config{}, "", nil
	}
	if err != nil {
		return Config{}, path, fmt.Errorf("integrations: read %s: %w", path, err)
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	// Unknown keys are errors: a typo like "calender" would
	// otherwise parse fine and silently leave the provider
	// disabled — Fritz then claims he can't do something the
	// user just enabled, with no hint anywhere.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			line, col := lineCol(data, syn.Offset)
			return Config{}, path, fmt.Errorf("integrations: parse %s: line %d, column %d: %w (fix the JSON; until then NO providers load)", path, line, col, err)
		}
		return Config{}, path, fmt.Errorf("integrations: parse %s: %w (fix the JSON; until then NO providers load)", path, err)
	}
	return cfg, path, nil
}

// lineCol converts a byte offset into 1-based line/column for
// error messages.
func lineCol(data []byte, offset int64) (line, col int) {
	line, col = 1, 1
	for i := int64(0); i < offset-1 && i < int64(len(data)); i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// Secret resolves an env-referenced secret. Empty name or unset
// variable names the problem for status reports — secret VALUES
// never travel through config or logs.
func Secret(envName string) (string, error) {
	if envName == "" {
		return "", fmt.Errorf("no env var configured")
	}
	value := os.Getenv(envName)
	if value == "" {
		return "", fmt.Errorf("%s is unset", envName)
	}
	return value, nil
}
