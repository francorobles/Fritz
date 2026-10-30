package meta

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// segmentStub serves one canned Responses SSE body without
// binding a port (transport-level fake; runs anywhere).
type segmentStub struct {
	body   string
	status int
	calls  int
	req    []byte
}

func (s *segmentStub) RoundTrip(req *http.Request) (*http.Response, error) {
	s.calls++
	s.req, _ = io.ReadAll(req.Body)
	code := s.status
	if code == 0 {
		code = http.StatusOK
	}
	return &http.Response{
		StatusCode: code,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(s.body)),
	}, nil
}

func TestChatImagePartsOnTheWire(t *testing.T) {
	stub := &segmentStub{body: "data: {\"choices\":[{\"delta\":{\"content\":\"Blue.\"}}]}\n\ndata: [DONE]\n\n"}
	client := &Client{APIKey: "k", HTTP: &http.Client{Transport: stub}}
	reply, err := client.ChatStream(context.Background(), []Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "what color is my shirt", Image: []byte("\xff\xd8jpeg")},
	}, func(delta string) {})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if reply != "Blue." {
		t.Errorf("reply = %q", reply)
	}
	body := string(stub.req)
	// The plain message stays a string; the photo message becomes parts.
	if !strings.Contains(body, `"content":"be brief"`) {
		t.Errorf("system message not a plain string: %s", body)
	}
	for _, needle := range []string{`"type":"text"`, `"text":"what color is my shirt"`, `"type":"image_url"`, "data:image/jpeg;base64,"} {
		if !strings.Contains(body, needle) {
			t.Errorf("request missing %s: %s", needle, body)
		}
	}
}

func TestSegmentStreamsDeltas(t *testing.T) {
	stub := &segmentStub{body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<0f>0<|box;x1=1;y1=2;x2=3;y2=4;w=10;h=10|>\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"<|mask;x=0;y=0;data=2,2,~xx|>\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n\n"}
	client := &Client{APIKey: "k", HTTP: &http.Client{Transport: stub}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := client.Segment(ctx, []byte("\xff\xd8jpeg"), "person")
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	want := "<0f>0<|box;x1=1;y1=2;x2=3;y2=4;w=10;h=10|><|mask;x=0;y=0;data=2,2,~xx|>"
	if out != want {
		t.Errorf("Segment = %q, want %q", out, want)
	}
	body := string(stub.req)
	for _, needle := range []string{`"sam-3.1"`, `"input_text"`, `"text":"person"`, "data:image/jpeg;base64,"} {
		if !strings.Contains(body, needle) {
			t.Errorf("request missing %s: %s", needle, body)
		}
	}
}

func TestSegmentZeroMatchesIsEmptyNotError(t *testing.T) {
	stub := &segmentStub{body: "data: {\"type\":\"response.completed\",\"response\":{}}\n\n"}
	client := &Client{APIKey: "k", HTTP: &http.Client{Transport: stub}}
	out, err := client.Segment(context.Background(), []byte("x"), "person")
	if err != nil || out != "" {
		t.Errorf("Segment zero matches = %q, %v, want empty, nil", out, err)
	}
}

func TestSegmentRetriesServerErrorOnce(t *testing.T) {
	stub := &segmentStub{status: http.StatusInternalServerError, body: `{"error":{"message":"boom"}}`}
	client := &Client{APIKey: "k", HTTP: &http.Client{Transport: stub}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.Segment(ctx, []byte("x"), "person"); err == nil {
		t.Fatal("expected an error on HTTP 500")
	}
	if stub.calls != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", stub.calls)
	}
}
