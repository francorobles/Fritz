package integrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Missing file: fresh install, everything disabled, no error.
	t.Setenv("FRITZ_INTEGRATIONS_FILE", filepath.Join(t.TempDir(), "nope.json"))
	cfg, path, err := Load()
	if err != nil || path != "" || cfg.Gmail.Enabled {
		t.Errorf("missing file: cfg=%+v path=%q err=%v", cfg, path, err)
	}

	// Malformed JSON is an error (silent zero config would hide typos).
	bad := filepath.Join(t.TempDir(), "integrations.json")
	_ = os.WriteFile(bad, []byte("{nope"), 0o600)
	t.Setenv("FRITZ_INTEGRATIONS_FILE", bad)
	if _, _, err := Load(); err == nil {
		t.Error("malformed JSON must error")
	}

	// The error must say WHERE, so a Pi edit can be fixed in one pass.
	badSyntax := filepath.Join(t.TempDir(), "integrations.json")
	_ = os.WriteFile(badSyntax, []byte("{\n  \"gmail\": {\"enabled\": true},\n  \"calendar\": {\"enabled\": true,}\n}"), 0o600)
	t.Setenv("FRITZ_INTEGRATIONS_FILE", badSyntax)
	if _, _, err := Load(); err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("trailing-comma error = %v, want it to name line 3", err)
	}

	// A misspelled provider key errors instead of silently disabling it.
	typo := filepath.Join(t.TempDir(), "integrations.json")
	_ = os.WriteFile(typo, []byte(`{"calender": {"enabled": true}}`), 0o600)
	t.Setenv("FRITZ_INTEGRATIONS_FILE", typo)
	if _, _, err := Load(); err == nil || !strings.Contains(err.Error(), "calender") {
		t.Errorf("typo error = %v, want it to name the unknown field", err)
	}

	// A real file parses, defaults resolve.
	good := filepath.Join(t.TempDir(), "integrations.json")
	_ = os.WriteFile(good, []byte(`{
		"workmail": {"enabled": true, "address": "franco@corp.example", "password_env": "FRITZ_WM_PW"},
		"whatsapp": {"enabled": true, "phone_number_id": "123", "allow": ["+63 917 555 0101"]}
	}`), 0o600)
	t.Setenv("FRITZ_INTEGRATIONS_FILE", good)
	cfg, path, err = Load()
	if err != nil || path != good {
		t.Fatalf("Load: path=%q err=%v", path, err)
	}
	if cfg.WorkMail.HostOrDefault() != "imap.mail.us-east-1.awsapps.com" {
		t.Errorf("workmail host = %q", cfg.WorkMail.HostOrDefault())
	}
	if cfg.WorkMail.PortOrDefault() != 993 {
		t.Errorf("workmail port = %d", cfg.WorkMail.PortOrDefault())
	}
	if cfg.Outlook.TenantOrDefault() != "common" {
		t.Errorf("tenant = %q", cfg.Outlook.TenantOrDefault())
	}
	if cfg.WhatsApp.AlertTemplateOrDefault() != "hello_world" || cfg.WhatsApp.GraphVersionOrDefault() != "v21.0" {
		t.Errorf("whatsapp defaults = %q/%q", cfg.WhatsApp.AlertTemplateOrDefault(), cfg.WhatsApp.GraphVersionOrDefault())
	}
}

func TestSecretResolution(t *testing.T) {
	if _, err := Secret(""); err == nil {
		t.Error("empty env name must error")
	}
	if _, err := Secret("FRITZ_DEFINITELY_UNSET_XYZ"); err == nil || !strings.Contains(err.Error(), "FRITZ_DEFINITELY_UNSET_XYZ") {
		t.Errorf("unset secret err = %v, want it to name the variable", err)
	}
	t.Setenv("FRITZ_TEST_SECRET", "s3cret")
	if v, err := Secret("FRITZ_TEST_SECRET"); err != nil || v != "s3cret" {
		t.Errorf("Secret = %q, %v", v, err)
	}
}

func TestRegistryStatuses(t *testing.T) {
	t.Setenv("FRITZ_CONFIG_DIR", t.TempDir())

	// Everything off: six statuses, none ready.
	reg := Build(Config{}, nil)
	if len(reg.Status) != 6 {
		t.Fatalf("statuses = %d, want 6", len(reg.Status))
	}
	for _, st := range reg.Status {
		if st.Ready || st.Enabled {
			t.Errorf("empty config: %+v must be off", st)
		}
	}

	// Enabled but unconfigured surfaces as not-ready with a reason.
	reg = Build(Config{
		Gmail:    GmailConfig{Enabled: true}, // no client/token files in temp dir
		Calendar: CalendarConfig{Enabled: true},
		WorkMail: WorkMailConfig{Enabled: true, Address: "a@b.example", PasswordEnv: "FRITZ_UNSET_PW"},
		WhatsApp: WhatsAppConfig{Enabled: true, PhoneNumberID: "1", TokenEnv: "FRITZ_UNSET_TOK"},
	}, nil)
	byName := map[string]ProviderStatus{}
	for _, st := range reg.Status {
		byName[st.Name] = st
	}
	if byName["gmail"].Ready || !strings.Contains(byName["gmail"].Detail, "googleauth") {
		t.Errorf("gmail status = %+v, want not-ready pointing at googleauth", byName["gmail"])
	}
	if byName["calendar"].Ready || !strings.Contains(byName["calendar"].Detail, "googleauth") {
		t.Errorf("calendar status = %+v, want not-ready pointing at googleauth", byName["calendar"])
	}
	if byName["workmail"].Ready || !strings.Contains(byName["workmail"].Detail, "FRITZ_UNSET_PW") {
		t.Errorf("workmail status = %+v", byName["workmail"])
	}
	if byName["whatsapp"].Ready {
		t.Errorf("whatsapp status = %+v, want not-ready (token unset)", byName["whatsapp"])
	}

	// WorkMail becomes ready with an address + password env.
	t.Setenv("FRITZ_WM_PW_OK", "pw")
	reg = Build(Config{WorkMail: WorkMailConfig{Enabled: true, Address: "a@b.example", PasswordEnv: "FRITZ_WM_PW_OK"}}, nil)
	if len(reg.Mailboxes) != 1 || reg.Mailboxes[0].Name() != "workmail" {
		t.Errorf("mailboxes = %v, want one workmail", reg.Mailboxes)
	}
}
