package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSecretTrimsLineEndings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("secret-value\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readSecret(path)
	if err != nil || got != "secret-value" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestReadSecretRejectsEmptyWithoutLeakingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readSecret(path)
	if err == nil || !strings.Contains(err.Error(), "secret is empty") {
		t.Fatalf("err=%v", err)
	}
}
