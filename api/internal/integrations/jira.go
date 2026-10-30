package integrations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Jira reads issues over the REST API. Cloud uses email + API
// token (Basic); Server/DC personal access tokens go Bearer
// (no email configured). Read-only this pass.
type Jira struct {
	base string
	http *http.Client
	auth string // full Authorization header value
}

// Issue is one Jira issue as lists show it.
type Issue struct {
	Key      string    `json:"key"`
	Summary  string    `json:"summary"`
	Status   string    `json:"status"`
	Assignee string    `json:"assignee"`
	Priority string    `json:"priority"`
	Updated  time.Time `json:"updated"`
}

// NewJira builds the client, resolving the env-referenced
// credentials. An unset email means "token is a PAT" (Bearer).
func NewJira(cfg JiraConfig) (*Jira, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("integrations: jira base_url is empty")
	}
	token, err := Secret(cfg.TokenEnv)
	if err != nil {
		return nil, fmt.Errorf("integrations: jira token: %w", err)
	}
	auth := "Bearer " + token
	if cfg.EmailEnv != "" {
		if email := os.Getenv(cfg.EmailEnv); email != "" {
			auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token))
		}
	}
	return &Jira{
		base: strings.TrimRight(cfg.BaseURL, "/"),
		http: &http.Client{Timeout: 30 * time.Second},
		auth: auth,
	}, nil
}

type jiraSearchResponse struct {
	Issues []struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
			Status  struct {
				Name string `json:"name"`
			} `json:"status"`
			Assignee *struct {
				DisplayName string `json:"displayName"`
			} `json:"assignee"`
			Priority *struct {
				Name string `json:"name"`
			} `json:"priority"`
			Updated string `json:"updated"`
		} `json:"fields"`
	} `json:"issues"`
}

// AssignedToMe returns open issues assigned to the token's user,
// most recently updated first — the "what's on my plate" query.
func (j *Jira) AssignedToMe(ctx context.Context, max int) ([]Issue, error) {
	return j.Search(ctx, "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC", max)
}

// Search runs an arbitrary JQL query.
func (j *Jira) Search(ctx context.Context, jql string, max int) ([]Issue, error) {
	if max <= 0 || max > 50 {
		max = 10
	}
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("maxResults", fmt.Sprint(max))
	q.Set("fields", "summary,status,assignee,priority,updated")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		j.base+"/rest/api/3/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", j.auth)
	req.Header.Set("Accept", "application/json")
	resp, err := j.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jira: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jira: HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	var out jiraSearchResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("jira: decode: %w", err)
	}
	issues := make([]Issue, 0, len(out.Issues))
	for _, raw := range out.Issues {
		issue := Issue{
			Key:     raw.Key,
			Summary: raw.Fields.Summary,
			Status:  raw.Fields.Status.Name,
			Updated: parseJiraTime(raw.Fields.Updated),
		}
		if raw.Fields.Assignee != nil {
			issue.Assignee = raw.Fields.Assignee.DisplayName
		}
		if raw.Fields.Priority != nil {
			issue.Priority = raw.Fields.Priority.Name
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// Comment is one issue comment, as plain text.
type Comment struct {
	Author  string
	Created time.Time
	Body    string
}

// IssueDetail is a fully-read issue: description and comments
// converted from Jira's ADF to plain text.
type IssueDetail struct {
	Issue
	Description string
	Comments    []Comment
}

type jiraIssueResponse struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  struct {
			Name string `json:"name"`
		} `json:"status"`
		Assignee *struct {
			DisplayName string `json:"displayName"`
		} `json:"assignee"`
		Priority *struct {
			Name string `json:"name"`
		} `json:"priority"`
		Updated     string          `json:"updated"`
		Description json.RawMessage `json:"description"`
		Comment     struct {
			Comments []struct {
				Author struct {
					DisplayName string `json:"displayName"`
				} `json:"author"`
				Created string          `json:"created"`
				Body    json.RawMessage `json:"body"`
			} `json:"comments"`
		} `json:"comment"`
	} `json:"fields"`
}

// GetIssue reads one issue in full (description + all comments).
func (j *Jira) GetIssue(ctx context.Context, key string) (IssueDetail, error) {
	path := "/rest/api/3/issue/" + url.PathEscape(key) +
		"?fields=summary,status,assignee,priority,updated,description,comment"
	var out jiraIssueResponse
	if err := j.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return IssueDetail{}, err
	}
	detail := IssueDetail{Description: adfFieldText(out.Fields.Description)}
	detail.Key = out.Key
	detail.Summary = out.Fields.Summary
	detail.Status = out.Fields.Status.Name
	detail.Updated = parseJiraTime(out.Fields.Updated)
	if out.Fields.Assignee != nil {
		detail.Assignee = out.Fields.Assignee.DisplayName
	}
	if out.Fields.Priority != nil {
		detail.Priority = out.Fields.Priority.Name
	}
	for _, c := range out.Fields.Comment.Comments {
		detail.Comments = append(detail.Comments, Comment{
			Author:  c.Author.DisplayName,
			Created: parseJiraTime(c.Created),
			Body:    adfFieldText(c.Body),
		})
	}
	return detail, nil
}

// CreateIssue files a new issue and returns its key. Callers
// must gate this behind an explicit confirmation (typed YES,
// spoken yes, or app Approve — never the model's say-so; see
// the confirmation rule in the fritz-agent skill).
func (j *Jira) CreateIssue(ctx context.Context, project, issueType, summary, description string) (string, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(summary) == "" {
		return "", fmt.Errorf("jira: project and summary are required")
	}
	if issueType == "" {
		issueType = "Task"
	}
	fields := map[string]any{
		"project":   map[string]string{"key": project},
		"summary":   summary,
		"issuetype": map[string]string{"name": issueType},
	}
	if strings.TrimSpace(description) != "" {
		fields["description"] = textToADF(description)
	}
	payload := map[string]any{"fields": fields}
	var out struct {
		Key string `json:"key"`
	}
	if err := j.do(ctx, http.MethodPost, "/rest/api/3/issue", payload, &out); err != nil {
		return "", err
	}
	if out.Key == "" {
		return "", fmt.Errorf("jira: create returned no key")
	}
	return out.Key, nil
}

// Transition is one workflow step an issue can take. IDs are
// workflow-specific (every Jira project differs), so they are
// always resolved from the server, never assumed.
type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   string `json:"-"` // target status name
}

type jiraTransitionsResponse struct {
	Transitions []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		To   struct {
			Name string `json:"name"`
		} `json:"to"`
	} `json:"transitions"`
}

// Transitions lists the workflow steps currently available for
// an issue (they depend on its current status).
func (j *Jira) Transitions(ctx context.Context, key string) ([]Transition, error) {
	var out jiraTransitionsResponse
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/transitions"
	if err := j.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	transitions := make([]Transition, 0, len(out.Transitions))
	for _, t := range out.Transitions {
		transitions = append(transitions, Transition{ID: t.ID, Name: t.Name, To: t.To.Name})
	}
	return transitions, nil
}

// FindTransition matches a user-typed name against available
// transitions, by transition name or target status (both
// case-insensitive): "done" finds a "Resolve" step that lands
// in Done. Pure function, split out for testing.
func FindTransition(transitions []Transition, name string) (Transition, bool) {
	name = strings.TrimSpace(name)
	for _, t := range transitions {
		if strings.EqualFold(t.Name, name) || strings.EqualFold(t.To, name) {
			return t, true
		}
	}
	return Transition{}, false
}

// Transition moves an issue via the named workflow step and
// returns the step taken. Like CreateIssue, callers must gate
// this behind an explicit confirmation.
func (j *Jira) Transition(ctx context.Context, key, name string) (Transition, error) {
	transitions, err := j.Transitions(ctx, key)
	if err != nil {
		return Transition{}, err
	}
	t, ok := FindTransition(transitions, name)
	if !ok {
		available := make([]string, len(transitions))
		for i, tr := range transitions {
			available[i] = fmt.Sprintf("%s → %s", tr.Name, tr.To)
		}
		return Transition{}, fmt.Errorf("jira: %s has no transition %q (available: %s)",
			key, name, strings.Join(available, ", "))
	}
	payload := map[string]any{"transition": map[string]string{"id": t.ID}}
	if err := j.do(ctx, http.MethodPost,
		"/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", payload, nil); err != nil {
		return Transition{}, err
	}
	return t, nil
}

// AddComment posts a comment and returns its id. Same
// confirmation duty on callers as CreateIssue.
func (j *Jira) AddComment(ctx context.Context, key, body string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("jira: comment body is empty")
	}
	payload := map[string]any{"body": textToADF(body)}
	var out struct {
		ID string `json:"id"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/comment"
	if err := j.do(ctx, http.MethodPost, path, payload, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// do performs one authenticated Jira call with a JSON body.
func (j *Jira) do(ctx context.Context, method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(data))
	}
	req, err := http.NewRequestWithContext(ctx, method, j.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", j.auth)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := j.http.Do(req)
	if err != nil {
		return fmt.Errorf("jira: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("jira: HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("jira: decode: %w", err)
		}
	}
	return nil
}

// adfFieldText converts an ADF (or plain-string) Jira rich-text
// field to plain text.
func adfFieldText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	return adfToText(doc)
}

// adfToText renders the subset of Atlassian Document Format
// that issue text uses: paragraphs, headings, lists, mentions,
// hard breaks. Marks (bold/links) keep their text only.
func adfToText(doc any) string {
	var b strings.Builder
	renderADF(&b, doc)
	out := strings.TrimSpace(b.String())
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return out
}

func renderADF(b *strings.Builder, node any) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	typ, _ := m["type"].(string)
	switch typ {
	case "text":
		if s, ok := m["text"].(string); ok {
			b.WriteString(s)
		}
		return
	case "hardBreak":
		b.WriteString("\n")
		return
	case "mention", "emoji":
		if attrs, ok := m["attrs"].(map[string]any); ok {
			for _, field := range []string{"text", "shortName"} {
				if s, ok := attrs[field].(string); ok && s != "" {
					b.WriteString(s)
					return
				}
			}
		}
		return
	}
	if typ == "listItem" {
		b.WriteString("- ")
	}
	if content, ok := m["content"].([]any); ok {
		for _, child := range content {
			renderADF(b, child)
		}
	}
	switch typ {
	case "paragraph", "heading", "codeBlock", "listItem", "blockquote", "rule", "table":
		b.WriteString("\n")
		if typ == "paragraph" || typ == "heading" || typ == "codeBlock" {
			b.WriteString("\n")
		}
	}
}

// textToADF wraps plain text as an ADF document (one paragraph
// per line) for issue creation.
func textToADF(text string) map[string]any {
	lines := strings.Split(text, "\n")
	content := make([]any, 0, len(lines))
	for _, line := range lines {
		para := map[string]any{"type": "paragraph"}
		if strings.TrimSpace(line) != "" {
			para["content"] = []any{map[string]any{"type": "text", "text": line}}
		}
		content = append(content, para)
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
}

// parseJiraTime accepts Jira's timestamp dialects (RFC 3339 and
// its colon-less-offset variant).
func parseJiraTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.000Z0700", "2006-01-02T15:04:05Z0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
