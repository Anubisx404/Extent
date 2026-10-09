package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// captureStreams runs fn with os.Stdout and os.Stderr redirected to pipes and
// returns what was written to each stream along with fn's error. The commands
// write directly to the process streams, so redirection is the only way to
// observe their output from a test.
func captureStreams(t *testing.T, fn func() error) (string, string, error) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW

	var outBuf, errBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&outBuf, outR) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&errBuf, errR) }()

	runErr := func() error {
		defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
		return fn()
	}()
	_ = outW.Close()
	_ = errW.Close()
	wg.Wait()
	_ = outR.Close()
	_ = errR.Close()
	return outBuf.String(), errBuf.String(), runErr
}

// unreachableURL returns a base URL whose port refuses connections, so backend
// queries fail fast and deterministically without depending on local services.
func unreachableURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	return url
}

// evidenceStub answers the Prometheus, Loki and Tempo query shapes used by the
// report and baseline commands with successful, non-empty responses, so the
// evidence collection completes offline.
func evidenceStub(t *testing.T) string {
	t.Helper()
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/query":
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"1"]}]}}`)
		case "/loki/api/v1/query_range":
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"streams","result":[]}}`)
		default:
			_, _ = io.WriteString(w, `{"traces":[],"batches":[]}`)
		}
	}))
	t.Cleanup(stub.Close)
	return stub.URL
}

// writeFixtureProject creates a minimal Node/Express project that the scan,
// analyze, plan, instrument and cardinality commands can read offline.
func writeFixtureProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"package.json": `{"name":"checkout","dependencies":{"express":"4.21.2"}}`,
		"server.js":    "const express = require('express')\nconst app = express()\napp.get('/health', (req, res) => res.send('ok'))\napp.listen(3000)\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
