package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/francorobles/fritz/api/internal/meta"
)

// TestEvalRunsAgainstFakeSpark checks the plumbing (batches, the
// watcher, scoring) without a real model: Spark always answers that
// the bank email is urgent and the weekly update is rule-held.
func TestEvalRunsAgainstFakeSpark(t *testing.T) {
	answer := `{"important":[{"id":"bank-fail","why":"payment failed","urgent":true,"hold":false},` +
		`{"id":"weekly-update","why":"project update","urgent":false,"hold":true}]}`
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%s},"finish_reason":"stop"}]}`, strconv.Quote(answer))
	}))
	defer srv.Close()
	old := meta.BaseURL
	meta.BaseURL = srv.URL
	defer func() { meta.BaseURL = old }()
	t.Setenv("MODEL_API_KEY", "fake")

	main()
	if calls != 6 {
		t.Errorf("judge calls = %d, want 6 batches", calls)
	}
}
