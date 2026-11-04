package integrations

import "context"

// ProviderStatus reports one provider's configuration state —
// mailcheck prints these and the server logs them, so a broken
// or half-configured provider is visible instead of silent.
type ProviderStatus struct {
	Name    string `json:"name"` // gmail | calendar | whatsapp
	Kind    string `json:"kind"` // mail | calendar | chat
	Enabled bool   `json:"enabled"`
	Ready   bool   `json:"ready"`
	Detail  string `json:"detail"`
}

// Registry is everything that connected. Mailboxes is what the
// agent's mail tools will read; WhatsAppHook is mounted by the
// HTTP server when non-nil (it needs the chat brain).
type Registry struct {
	Mailboxes    []Mailbox
	Calendar     *Calendar
	WhatsApp     *WhatsApp
	WhatsAppHook *Webhook
	Status       []ProviderStatus
}

// Build constructs every enabled provider, capturing failures
// per provider instead of failing the world: a Fritz with a
// broken Gmail token must still answer on WhatsApp.
// brain may be nil (no chat hook is assembled then).
func Build(cfg Config, brain Replier) *Registry {
	reg := &Registry{}
	add := func(s ProviderStatus) { reg.Status = append(reg.Status, s) }

	// Gmail
	st := ProviderStatus{Name: "gmail", Kind: "mail", Enabled: cfg.Gmail.Enabled}
	switch {
	case !cfg.Gmail.Enabled:
		st.Detail = "disabled"
	default:
		if g, err := NewGmail(context.Background()); err != nil {
			st.Detail = err.Error()
		} else {
			reg.Mailboxes = append(reg.Mailboxes, g)
			st.Ready, st.Detail = true, "OAuth token loaded (live check in mailcheck)"
		}
	}
	add(st)

	// Google Calendar (shares Gmail's OAuth token)
	st = ProviderStatus{Name: "calendar", Kind: "calendar", Enabled: cfg.Calendar.Enabled}
	switch {
	case !cfg.Calendar.Enabled:
		st.Detail = "disabled"
	default:
		if cal, err := NewGoogleCalendar(context.Background()); err != nil {
			st.Detail = err.Error()
		} else {
			reg.Calendar = cal
			st.Ready, st.Detail = true, "shares the Gmail OAuth token (live check in mailcheck)"
		}
	}
	add(st)

	// WhatsApp (Cloud API, two-way)
	st = ProviderStatus{Name: "whatsapp", Kind: "chat", Enabled: cfg.WhatsApp.Enabled}
	switch {
	case !cfg.WhatsApp.Enabled:
		st.Detail = "disabled"
	case cfg.WhatsApp.PhoneNumberID == "":
		st.Detail = "phone_number_id is empty"
	default:
		token, err := Secret(cfg.WhatsApp.TokenEnv)
		switch {
		case err != nil:
			st.Detail = err.Error()
		case len(cfg.WhatsApp.Allow) == 0:
			st.Detail = "allow list is empty — nobody may chat with Fritz"
		default:
			wa := NewWhatsApp(cfg.WhatsApp, token)
			reg.WhatsApp = wa
			st.Ready = true
			st.Detail = "Cloud API ready"
			if brain != nil {
				verifyToken, _ := Secret(cfg.WhatsApp.VerifyTokenEnv)
				appSecret, _ := Secret(cfg.WhatsApp.AppSecretEnv)
				reg.WhatsAppHook = &Webhook{
					Client:      wa,
					VerifyToken: verifyToken,
					AppSecret:   appSecret,
					Allow:       cfg.WhatsApp.Allow,
					Brain:       brain,
				}
				if verifyToken == "" {
					st.Detail += "; webhook verify token env unset (GET challenge will fail)"
				}
				if appSecret == "" {
					st.Detail += "; app secret unset (signatures not verified)"
				}
			} else {
				st.Detail += "; no chat brain (webhook not mounted)"
			}
		}
	}
	add(st)

	return reg
}
