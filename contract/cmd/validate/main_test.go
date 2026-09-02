package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	specPath   = "../../../docs/api/openapi.yaml"
	backendDir = "../../../backend/internal/testutil/testdata"
)

// The real spec + committed fixtures must pass with zero violations. This is
// the M0 gate itself, run as a unit test.
func TestBackendFixturesSatisfyContract(t *testing.T) {
	rep, err := run(specPath, backendDir)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(rep.violations) != 0 {
		t.Fatalf("expected no violations, got:\n  %s", strings.Join(rep.violations, "\n  "))
	}
	if len(rep.ok) < 2 {
		t.Fatalf("expected a spec-validates line and a coverage line, got %v", rep.ok)
	}
}

// A fixture whose body breaks the schema must be reported.
func TestSchemaViolationIsCaught(t *testing.T) {
	dir := copyFixtures(t)
	write(t, filepath.Join(dir, "pairDevice.request.json"), `{"code":"NOPE","deviceName":"x","platform":"android","appVersionCode":1}`)

	rep, err := run(specPath, dir)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !containsSubstr(rep.violations, "POST /pair request pairDevice.request.json") {
		t.Fatalf("pattern violation not reported: %v", rep.violations)
	}
}

// Deleting a fixture must trip the "fixtures exist for every endpoint" check.
func TestMissingFixtureIsCaught(t *testing.T) {
	dir := copyFixtures(t)
	if err := os.Remove(filepath.Join(dir, "getSystemStatus.response-200.json")); err != nil {
		t.Fatal(err)
	}

	rep, err := run(specPath, dir)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !containsSubstr(rep.violations, "getSystemStatus.response-200.json") {
		t.Fatalf("missing fixture not reported: %v", rep.violations)
	}
}

// An operation the manifest forgets entirely must be reported as uncovered.
func TestUncoveredOperationIsCaught(t *testing.T) {
	dir := copyFixtures(t)
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.Replace(string(raw), `"operationId": "getUpdateStatus"`, `"operationId": "getUpdateStatusXXX"`, 1)
	trimmed = strings.Replace(trimmed, `"path": "/system/update"`, `"path": "/system/updateXXX"`, 1)
	write(t, filepath.Join(dir, "manifest.json"), trimmed)

	rep, err := run(specPath, dir)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !containsSubstr(rep.violations, "GET /system/update") {
		t.Fatalf("uncovered operation not reported: %v", rep.violations)
	}
}

func copyFixtures(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir(backendDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(backendDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dst, e.Name()), string(b))
	}
	return dst
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsSubstr(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
