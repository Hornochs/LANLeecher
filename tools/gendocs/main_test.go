package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedDocsAreCurrent fails when config.go, llms.txt, the docs or the
// OpenAPI spec changed without regenerating.
func TestGeneratedDocsAreCurrent(t *testing.T) {
	files, err := Generate("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		old, err := os.ReadFile(filepath.Join("../..", f.Path))
		if err != nil || !bytes.Equal(old, f.Content) {
			t.Errorf("%s is out of date; run: go run ./tools/gendocs", f.Path)
		}
	}
}
