package integrations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func fakeJira(t *testing.T, wantAuthPrefix string) *Jira {
	t.Helper()
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		if auth := req.Header.Get("Authorization"); !strings.HasPrefix(auth, wantAuthPrefix) {
			t.Errorf("Authorization = %q, want %s...", auth, wantAuthPrefix)
		}
		if !strings.Contains(req.URL.Path, "/rest/api/3/search") {
			t.Errorf("path = %q", req.URL.Path)
		}
		return jsonResponse(200, `{"issues": [{
			"key": "FRITZ-7",
			"fields": {
				"summary": "Wire up the thing",
				"status": {"name": "In Progress"},
				"assignee": {"displayName": "Franco Robles"},
				"priority": {"name": "High"},
				"updated": "2030-10-15T09:30:00.000+0800"
			}}]}`), nil
	})
	j, err := NewJira(JiraConfig{
		Enabled:  true,
		BaseURL:  "https://corp.example.net/",
		EmailEnv: "FRITZ_JIRA_EMAIL_T",
		TokenEnv: "FRITZ_JIRA_TOKEN_T",
	})
	if err != nil {
		t.Fatal(err)
	}
	j.http = &http.Client{Transport: transport}
	j.base = "https://corp.example.net"
	return j
}

func TestJiraAssignedToMe(t *testing.T) {
	t.Setenv("FRITZ_JIRA_EMAIL_T", "franco@corp.example")
	t.Setenv("FRITZ_JIRA_TOKEN_T", "tok123")
	j := fakeJira(t, "Basic ")
	issues, err := j.AssignedToMe(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Key != "FRITZ-7" || issues[0].Status != "In Progress" {
		t.Errorf("issues = %+v", issues)
	}
	if issues[0].Updated.Year() != 2030 || issues[0].Assignee != "Franco Robles" {
		t.Errorf("issue fields = %+v (colon-less offset date must parse)", issues[0])
	}
}

func TestJiraBearerWhenNoEmail(t *testing.T) {
	t.Setenv("FRITZ_JIRA_TOKEN_T", "pat456")
	// EmailEnv names an unset variable: PAT mode (Bearer).
	j, err := NewJira(JiraConfig{
		Enabled:  true,
		BaseURL:  "https://jira.corp.example",
		EmailEnv: "FRITZ_JIRA_EMAIL_UNSET_T",
		TokenEnv: "FRITZ_JIRA_TOKEN_T",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(j.auth, "Bearer ") {
		t.Errorf("auth = %q, want Bearer", j.auth)
	}
}

func TestJiraGetIssue(t *testing.T) {
	t.Setenv("FRITZ_JIRA_TOKEN_T", "tok123")
	j, err := NewJira(JiraConfig{Enabled: true, BaseURL: "https://corp.example.net", TokenEnv: "FRITZ_JIRA_TOKEN_T"})
	if err != nil {
		t.Fatal(err)
	}
	j.http = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/rest/api/3/issue/FRITZ-7") {
			t.Errorf("path = %q", req.URL.Path)
		}
		return jsonResponse(200, `{
			"key": "FRITZ-7",
			"fields": {
				"summary": "Wire up the thing",
				"status": {"name": "In Progress"},
				"assignee": {"displayName": "Franco Robles"},
				"priority": {"name": "High"},
				"updated": "2030-10-15T09:30:00.000+0800",
				"description": {"type": "doc", "version": 1, "content": [
					{"type": "paragraph", "content": [
						{"type": "text", "text": "First line."},
						{"type": "hardBreak"},
						{"type": "text", "text": "Second line."}
					]},
					{"type": "bulletList", "content": [
						{"type": "listItem", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "one"}]}]},
						{"type": "listItem", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "two"}]}]}
					]}
				]},
				"comment": {"comments": [{
					"author": {"displayName": "Sarah"},
					"created": "2030-10-15T10:00:00.000+0800",
					"body": {"type": "doc", "version": 1, "content": [
						{"type": "paragraph", "content": [{"type": "text", "text": "Looks good."}]}
					]}
				}]}
			}}`), nil
	})}

	detail, err := j.GetIssue(context.Background(), "FRITZ-7")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary != "Wire up the thing" || detail.Status != "In Progress" {
		t.Errorf("detail = %+v", detail.Issue)
	}
	if !strings.Contains(detail.Description, "First line.\nSecond line.") ||
		!strings.Contains(detail.Description, "- one") || !strings.Contains(detail.Description, "- two") {
		t.Errorf("description = %q, want ADF rendered as text with bullets", detail.Description)
	}
	if len(detail.Comments) != 1 || detail.Comments[0].Author != "Sarah" || detail.Comments[0].Body != "Looks good." {
		t.Errorf("comments = %+v", detail.Comments)
	}
}

func TestJiraCreateIssue(t *testing.T) {
	t.Setenv("FRITZ_JIRA_TOKEN_T", "tok123")
	j, err := NewJira(JiraConfig{Enabled: true, BaseURL: "https://corp.example.net", TokenEnv: "FRITZ_JIRA_TOKEN_T"})
	if err != nil {
		t.Fatal(err)
	}
	var captured map[string]any
	j.http = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/rest/api/3/issue") {
			t.Errorf("%s %s, want POST issue create", req.Method, req.URL.Path)
		}
		body, _ := io.ReadAll(req.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("create body not JSON: %v", err)
		}
		return jsonResponse(201, `{"id":"10001","key":"FRITZ-42"}`), nil
	})}

	key, err := j.CreateIssue(context.Background(), "FRITZ", "Task", "Fix the gaze", "It drifts left.\nCheck the mount.")
	if err != nil {
		t.Fatal(err)
	}
	if key != "FRITZ-42" {
		t.Errorf("key = %q, want FRITZ-42", key)
	}
	fields, _ := captured["fields"].(map[string]any)
	proj, _ := fields["project"].(map[string]any)
	issueType, _ := fields["issuetype"].(map[string]any)
	desc, _ := fields["description"].(map[string]any)
	if proj["key"] != "FRITZ" || fields["summary"] != "Fix the gaze" || issueType["name"] != "Task" || desc["type"] != "doc" {
		t.Errorf("create payload = %v, want project/summary/type + ADF description", captured)
	}
	if _, err := j.CreateIssue(context.Background(), "", "Task", "x", ""); err == nil {
		t.Error("empty project must error before any call")
	}
}

func TestJiraTransitions(t *testing.T) {
	t.Setenv("FRITZ_JIRA_TOKEN_T", "tok123")
	j, err := NewJira(JiraConfig{Enabled: true, BaseURL: "https://corp.example.net", TokenEnv: "FRITZ_JIRA_TOKEN_T"})
	if err != nil {
		t.Fatal(err)
	}
	var posted map[string]any
	j.http = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			body, _ := io.ReadAll(req.Body)
			if err := json.Unmarshal(body, &posted); err != nil {
				t.Errorf("transition body not JSON: %v", err)
			}
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		if !strings.HasSuffix(req.URL.Path, "/transitions") {
			t.Errorf("path = %q", req.URL.Path)
		}
		return jsonResponse(200, `{"transitions": [
			{"id": "21", "name": "Start Progress", "to": {"name": "In Progress"}},
			{"id": "31", "name": "Resolve", "to": {"name": "Done"}}
		]}`), nil
	})}

	transitions, err := j.Transitions(context.Background(), "FRITZ-7")
	if err != nil || len(transitions) != 2 || transitions[1].To != "Done" {
		t.Fatalf("Transitions = %+v, %v", transitions, err)
	}
	// Matched by target status, case-insensitively.
	fired, err := j.Transition(context.Background(), "FRITZ-7", "done")
	if err != nil {
		t.Fatal(err)
	}
	if fired.ID != "31" {
		t.Errorf("fired = %+v, want the Resolve step (id 31)", fired)
	}
	body, _ := posted["transition"].(map[string]any)
	if body["id"] != "31" {
		t.Errorf("posted = %v, want transition id 31", posted)
	}
	// Unknown names error with what's available, and fire nothing.
	posted = nil
	if _, err := j.Transition(context.Background(), "FRITZ-7", "ship it"); err == nil ||
		!strings.Contains(err.Error(), "Start Progress") {
		t.Errorf("unknown transition err = %v, want it to list available steps", err)
	}
	if _, ok := FindTransition(transitions, " Start Progress "); !ok {
		t.Error("FindTransition must trim + case-fold names")
	}
}

func TestJiraAddComment(t *testing.T) {
	t.Setenv("FRITZ_JIRA_TOKEN_T", "tok123")
	j, err := NewJira(JiraConfig{Enabled: true, BaseURL: "https://corp.example.net", TokenEnv: "FRITZ_JIRA_TOKEN_T"})
	if err != nil {
		t.Fatal(err)
	}
	var captured map[string]any
	j.http = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/issue/FRITZ-7/comment") {
			t.Errorf("%s %s, want POST comment", req.Method, req.URL.Path)
		}
		body, _ := io.ReadAll(req.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("comment body not JSON: %v", err)
		}
		return jsonResponse(201, `{"id":"10099"}`), nil
	})}

	id, err := j.AddComment(context.Background(), "FRITZ-7", "Looks good, shipping it.")
	if err != nil {
		t.Fatal(err)
	}
	if id != "10099" {
		t.Errorf("comment id = %q, want 10099", id)
	}
	bodyDoc, _ := captured["body"].(map[string]any)
	if bodyDoc["type"] != "doc" {
		t.Errorf("comment payload = %v, want ADF body", captured)
	}
	if _, err := j.AddComment(context.Background(), "FRITZ-7", "   "); err == nil {
		t.Error("empty comment must error before any call")
	}
}

func TestADFToText(t *testing.T) {
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{"type": "paragraph", "content": []any{
				map[string]any{"type": "text", "text": "Hi "},
				map[string]any{"type": "mention", "attrs": map[string]any{"text": "@sarah"}},
				map[string]any{"type": "text", "text": "!"},
			}},
		},
	}
	if got := adfToText(doc); got != "Hi @sarah!" {
		t.Errorf("adfToText = %q", got)
	}
	if got := adfFieldText(json.RawMessage(`"plain string body"`)); got != "plain string body" {
		t.Errorf("string field = %q, want passthrough", got)
	}
	if got := adfFieldText(json.RawMessage(`null`)); got != "" {
		t.Errorf("null field = %q, want empty", got)
	}
}

func TestJiraErrors(t *testing.T) {
	t.Setenv("FRITZ_JIRA_TOKEN_T", "bad")
	j, err := NewJira(JiraConfig{Enabled: true, BaseURL: "https://corp.example.net", TokenEnv: "FRITZ_JIRA_TOKEN_T"})
	if err != nil {
		t.Fatal(err)
	}
	j.http = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(401, `{"errorMessages":["You are not authenticated"]}`), nil
	})}
	if _, err := j.AssignedToMe(context.Background(), 5); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want a 401 error", err)
	}
	if _, err := NewJira(JiraConfig{Enabled: true, TokenEnv: "FRITZ_JIRA_TOKEN_T"}); err == nil {
		t.Error("empty base_url must error")
	}
}
