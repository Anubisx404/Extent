package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writeReadOnlyFixture creates a small Node express repository for the
// read-only guarantee test.
func writeReadOnlyFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"package.json": `{
  "name": "checkout",
  "version": "1.0.0",
  "scripts": {"start": "node server.js", "test": "vitest"},
  "dependencies": {"express": "^4.18.0", "pino": "^9.0.0", "axios": "^1.0.0"}
}
`,
		"server.js": `const express = require("express");
const app = express();
app.get("/health", (req, res) => res.send("ok"));
app.post("/checkout", (req, res) => res.send("done"));
app.listen(8080);
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// hashTree returns a deterministic digest of every path, entry type, file
// mode and file content under root.
func hashTree(t *testing.T, root string) string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			entries = append(entries, "dir "+rel+" "+info.Mode().String())
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		entries = append(entries, "file "+rel+" "+info.Mode().String()+" "+hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return hex.EncodeToString(sum[:])
}

func TestReadOnlyCommandsDoNotModifyTargetRepository(t *testing.T) {
	root := t.TempDir()
	writeReadOnlyFixture(t, root)
	before := hashTree(t, root)

	commands := []struct {
		name string
		args []string
		must bool
	}{
		{"scan", []string{"scan", "--json", root}, true},
		{"analyze", []string{"analyze", "--json", root}, true},
		{"plan", []string{"plan", "--json", root}, true},
		{"doctor", []string{"doctor", "--json", root}, false},
		{"cardinality", []string{"cardinality", "--json", root}, true},
	}
	for _, c := range commands {
		err := run(c.args)
		if c.must && err != nil {
			t.Fatalf("%s failed: %v", c.name, err)
		}
		if after := hashTree(t, root); after != before {
			t.Fatalf("%s modified the target repository", c.name)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".extent")); !os.IsNotExist(err) {
		t.Fatalf("read-only commands created .extent/: stat err = %v", err)
	}
}
