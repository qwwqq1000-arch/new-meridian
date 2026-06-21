package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadToken(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"acc","refreshToken":"ref","expiresAt":123}}`), 0600)
	acc, ref, exp, err := ReadToken(dir)
	if err != nil || acc != "acc" || ref != "ref" || exp != 123 {
		t.Fatalf("got %q %q %d %v", acc, ref, exp, err)
	}
}

func TestRefreshToken(t *testing.T) {
	post := func(url string, body []byte) ([]byte, int, error) {
		return []byte(`{"access_token":"newacc","refresh_token":"newref","expires_in":3600}`), 200, nil
	}
	acc, ref, _, err := RefreshToken("ref", post)
	if err != nil || acc != "newacc" || ref != "newref" {
		t.Fatalf("got %q %q %v", acc, ref, err)
	}
}
