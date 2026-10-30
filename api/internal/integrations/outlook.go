package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"
)

// Outlook reads company mail through Microsoft Graph with a
// delegated token: the user signs in as themselves via
// cmd/outlookauth (device code), so no IT admin consent is
// needed and Fritz can only ever see that one mailbox.
// Read-only this pass (Mail.Read); sending arrives with the
// agent's confirmation flow.
type Outlook struct {
	http *http.Client
	base string
}

// graphBase is overridable in tests.
const graphBase = "https://graph.microsoft.com/v1.0"

// OutlookScopes are the delegated scopes Fritz requests.
var OutlookScopes = []string{"Mail.Read", "offline_access"}

// NewOutlook builds the client from outlook-token.json (saved
// by cmd/outlookauth), auto-refreshing and persisting rotations.
func NewOutlook(cfg OutlookConfig) (*Outlook, error) {
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("integrations: outlook client_id is empty (register an app in Azure AD; see cmd/outlookauth)")
	}
	tok, err := loadTokenFile(OutlookTokenFile())
	if err != nil {
		return nil, fmt.Errorf("integrations: outlook token: %w (run: go run ./cmd/outlookauth)", err)
	}
	oc := oauth2.Config{
		ClientID: cfg.ClientID,
		Endpoint: microsoft.AzureADEndpoint(cfg.TenantOrDefault()),
		Scopes:   OutlookScopes,
	}
	src := newPersistingTokenSource(oc.TokenSource(context.Background(), tok), OutlookTokenFile())
	return &Outlook{http: oauth2.NewClient(context.Background(), src), base: graphBase}, nil
}

// Name implements Mailbox.
func (o *Outlook) Name() string { return "outlook" }

type graphList struct {
	Value []graphMessage `json:"value"`
}

type graphMessage struct {
	ID               string    `json:"id"`
	Subject          string    `json:"subject"`
	ReceivedDateTime time.Time `json:"receivedDateTime"`
	BodyPreview      string    `json:"bodyPreview"`
	IsRead           bool      `json:"isRead"`
	From             struct {
		EmailAddress struct {
			Name    string `json:"name"`
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"from"`
	Body struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
}

func graphSummary(m graphMessage) Summary {
	from := m.From.EmailAddress.Address
	if name := m.From.EmailAddress.Name; name != "" && name != from {
		from = fmt.Sprintf("%s <%s>", name, from)
	}
	return Summary{
		Account: "outlook",
		ID:      m.ID,
		From:    from,
		Subject: m.Subject,
		Date:    m.ReceivedDateTime,
		Snippet: m.BodyPreview,
		Unread:  !m.IsRead,
	}
}

// Search implements Mailbox via Graph $search (free text) or a
// newest-first list when the query is empty.
func (o *Outlook) Search(ctx context.Context, query string, max int) ([]Summary, error) {
	if max <= 0 || max > 25 {
		max = 10
	}
	q := url.Values{}
	q.Set("$top", fmt.Sprint(max))
	q.Set("$select", "id,from,subject,receivedDateTime,bodyPreview,isRead")
	searching := strings.TrimSpace(query) != ""
	if searching {
		// Graph $search needs the value quoted and forbids $orderby.
		q.Set("$search", `"`+strings.ReplaceAll(query, `"`, `\"`)+`"`)
	} else {
		q.Set("$orderby", "receivedDateTime desc")
	}
	var list graphList
	err := o.get(ctx, "/me/messages?"+q.Encode(), searching, &list)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(list.Value))
	for _, m := range list.Value {
		out = append(out, graphSummary(m))
	}
	return out, nil
}

// Read implements Mailbox.
func (o *Outlook) Read(ctx context.Context, id string) (Message, error) {
	var m graphMessage
	path := "/me/messages/" + url.PathEscape(id) + "?$select=id,from,subject,receivedDateTime,bodyPreview,isRead,body"
	if err := o.get(ctx, path, false, &m); err != nil {
		return Message{}, err
	}
	body := m.Body.Content
	if strings.EqualFold(m.Body.ContentType, "html") {
		body = htmlToText(body)
	}
	return Message{Summary: graphSummary(m), Body: stripQuotedReply(body)}, nil
}

// get performs one authenticated Graph call. withSearch adds the
// ConsistencyLevel header $search requires.
func (o *Outlook) get(ctx context.Context, path string, withSearch bool, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base+path, nil)
	if err != nil {
		return err
	}
	if withSearch {
		req.Header.Set("ConsistencyLevel", "eventual")
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return fmt.Errorf("outlook: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("outlook: HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("outlook: decode: %w", err)
	}
	return nil
}
