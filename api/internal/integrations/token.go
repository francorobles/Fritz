package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
)

// saveTokenFile writes an OAuth token at mode 0600 (it is a
// credential; the config dir must never be group/world-readable
// for these).
func saveTokenFile(path string, tok *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func loadTokenFile(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// persistingTokenSource wraps a refreshing TokenSource and saves
// the token whenever the access token changes, so refresh tokens
// survive restarts (Google rotates them).
type persistingTokenSource struct {
	src  oauth2.TokenSource
	path string
	last string
}

func newPersistingTokenSource(src oauth2.TokenSource, path string) oauth2.TokenSource {
	return &persistingTokenSource{src: src, path: path}
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := s.src.Token()
	if err == nil && tok.AccessToken != "" && tok.AccessToken != s.last {
		s.last = tok.AccessToken
		_ = saveTokenFile(s.path, tok) // best effort; a failed save only costs a re-login
	}
	return tok, err
}
