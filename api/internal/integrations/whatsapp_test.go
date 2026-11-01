package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func fakeWhatsApp(t *testing.T, capture *map[string]any) *WhatsApp {
	t.Helper()
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		if capture != nil {
			*capture = map[string]any{}
			if err := json.Unmarshal(body, capture); err != nil {
				t.Errorf("payload not JSON: %v", err)
			}
		}
		if !strings.HasSuffix(req.URL.Path, "/v21.0/123/messages") {
			t.Errorf("url path = %q", req.URL.Path)
		}
		if auth := req.Header.Get("Authorization"); auth != "Bearer tok" {
			t.Errorf("Authorization = %q", auth)
		}
		return jsonResponse(200, `{"messages":[{"id":"wamid.1"}]}`), nil
	})
	return &WhatsApp{
		http:    &http.Client{Transport: transport},
		base:    "https://graph.example",
		version: "v21.0",
		phoneID: "123",
		token:   "tok",
	}
}

func TestWhatsAppSendTextAndTemplate(t *testing.T) {
	var payload map[string]any
	wa := fakeWhatsApp(t, &payload)
	if err := wa.SendText(context.Background(), "+63 917 555 0101", "Hi from Fritz"); err != nil {
		t.Fatal(err)
	}
	if payload["to"] != "639175550101" || payload["type"] != "text" {
		t.Errorf("SendText payload = %v, want digits-only number + text type", payload)
	}
	if err := wa.SendTemplate(context.Background(), "639175550101", "alert", "en_US", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if payload["type"] != "template" {
		t.Errorf("SendTemplate payload = %v", payload)
	}
}

func TestWhatsAppSendError(t *testing.T) {
	wa := fakeWhatsApp(t, nil)
	wa.http = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(400, `{"error":{"message":"Invalid OAuth access token","code":190}}`), nil
	})}
	err := wa.SendText(context.Background(), "1", "x")
	if err == nil || !strings.Contains(err.Error(), "Invalid OAuth") {
		t.Errorf("err = %v, want the Graph error surfaced", err)
	}
}

const webhookBody = `{
	"entry": [{
		"changes": [{
			"value": {
				"messages": [{
					"id": "wamid.HBg1", "from": "639175550101",
					"timestamp": "1792000000", "type": "text",
					"text": {"body": "Hi Fritz, what is on my plate today?"}
				}],
				"statuses": [{"id": "wamid.old", "status": "delivered"}]
			}
		}]
	}]
}`

func TestParseInbound(t *testing.T) {
	msgs, err := ParseInbound([]byte(webhookBody))
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].From != "639175550101" || !strings.Contains(msgs[0].Text, "what is on my plate") {
		t.Errorf("msgs = %+v, want exactly the one text message (statuses ignored)", msgs)
	}
	if msgs[0].Timestamp.Unix() != 1792000000 {
		t.Errorf("timestamp = %v", msgs[0].Timestamp)
	}
	if _, err := ParseInbound([]byte("{oops")); err == nil {
		t.Error("malformed body must error")
	}
}

func TestSignatureAndAllowlist(t *testing.T) {
	secret := "appsecret"
	body := []byte(webhookBody)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	header := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !ValidSignature(secret, body, header) {
		t.Error("valid signature rejected")
	}
	if ValidSignature(secret, body, "sha256=deadbeef") {
		t.Error("invalid signature accepted")
	}
	if ValidSignature("", body, header) {
		t.Error("empty secret must fail closed")
	}
	if !AllowedNumber([]string{"+63 917 555 0101"}, "639175550101") {
		t.Error("owner number must match in any format")
	}
	if AllowedNumber([]string{"+63 917 555 0101"}, "639999999999") || AllowedNumber(nil, "639175550101") {
		t.Error("strangers and empty allowlists must be refused")
	}
	if challenge, ok := VerifySubscription("subscribe", "vtok", "42", "vtok"); !ok || challenge != "42" {
		t.Errorf("verify = %q, %v", challenge, ok)
	}
	if _, ok := VerifySubscription("subscribe", "wrong", "42", "vtok"); ok {
		t.Error("wrong verify token must fail")
	}
}

// Sentry photos: upload the JPEG as multipart, then send it by id.
func TestWhatsAppUploadAndSendImage(t *testing.T) {
	var sent map[string]any
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/v21.0/123/media"):
			if err := req.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("media upload not multipart: %v", err)
			}
			if req.FormValue("messaging_product") != "whatsapp" || req.FormValue("type") != "image/jpeg" {
				t.Errorf("form = %v", req.MultipartForm.Value)
			}
			f, hdr, err := req.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(f)
			if string(data) != "JPEG" || hdr.Header.Get("Content-Type") != "image/jpeg" {
				t.Errorf("file = %q (%s)", data, hdr.Header.Get("Content-Type"))
			}
			return jsonResponse(200, `{"id":"media-9"}`), nil
		case strings.HasSuffix(req.URL.Path, "/v21.0/123/messages"):
			body, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(body, &sent)
			return jsonResponse(200, `{"messages":[{"id":"wamid.2"}]}`), nil
		}
		t.Errorf("unexpected path %s", req.URL.Path)
		return jsonResponse(404, `{}`), nil
	})
	wa := &WhatsApp{http: &http.Client{Transport: transport}, base: "https://graph.example", version: "v21.0", phoneID: "123", token: "tok"}
	id, err := wa.UploadMedia(context.Background(), []byte("JPEG"))
	if err != nil || id != "media-9" {
		t.Fatalf("UploadMedia = %q, %v", id, err)
	}
	if err := wa.SendImage(context.Background(), "+63 917 000 0000", id, "Someone's at your desk"); err != nil {
		t.Fatal(err)
	}
	img, _ := sent["image"].(map[string]any)
	if sent["type"] != "image" || sent["to"] != "639170000000" || img["id"] != "media-9" || img["caption"] != "Someone's at your desk" {
		t.Errorf("image payload = %v", sent)
	}
}
