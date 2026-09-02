// Command validate is the WP-0 contract gate.
//
// It (1) structurally validates docs/api/openapi.yaml and (2) checks that the
// shared fixture directory holds one request/response example for every
// operation and status the spec declares, with every JSON-bodied fixture
// conforming to the resolved OpenAPI schema.
//
// Both CI lanes run this binary: the backend lane against
// backend/internal/testutil/testdata, the client lane against the copy synced
// into client/app/src/test/resources/fixtures. Same bytes, same gate.
//
//	go run ./cmd/validate -spec ../docs/api/openapi.yaml -fixtures ../backend/internal/testutil/testdata
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

func main() {
	specPath := flag.String("spec", "../docs/api/openapi.yaml", "path to openapi.yaml")
	fixturesDir := flag.String("fixtures", "../backend/internal/testutil/testdata", "fixture directory containing manifest.json")
	flag.Parse()

	report, err := run(*specPath, *fixturesDir)
	for _, line := range report.ok {
		fmt.Println(line)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL %v\n", err)
		os.Exit(1)
	}
	if len(report.violations) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d contract violation(s):\n", len(report.violations))
		for _, e := range report.violations {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		os.Exit(1)
	}
}

type report struct {
	ok         []string
	violations []string
}

type manifest struct {
	Operations []struct {
		OperationID string `json:"operationId"`
		Method      string `json:"method"`
		Path        string `json:"path"`
		Request     *struct {
			File string `json:"file"`
			Kind string `json:"kind"`
		} `json:"request"`
		Responses []struct {
			Status string `json:"status"`
			File   string `json:"file"`
			Kind   string `json:"kind"`
		} `json:"responses"`
	} `json:"operations"`
}

// run loads and structurally validates the spec, then checks fixture coverage
// and conformance. A returned error means the spec or manifest could not be
// loaded at all; per-fixture problems come back in report.violations.
func run(specPath, fixturesDir string) (report, error) {
	var rep report

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		return rep, fmt.Errorf("load %s: %w", specPath, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		return rep, fmt.Errorf("openapi.yaml is not valid: %w", err)
	}
	rep.ok = append(rep.ok, fmt.Sprintf("ok   openapi.yaml validates (%d paths)", doc.Paths.Len()))

	m, err := loadManifest(filepath.Join(fixturesDir, "manifest.json"))
	if err != nil {
		return rep, err
	}

	v := &validator{fixturesDir: fixturesDir}

	// Every operation the spec declares, keyed "METHOD /path".
	specOps := map[string]*openapi3.Operation{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			specOps[method+" "+path] = op
		}
	}

	covered := map[string]bool{}
	for _, mop := range m.Operations {
		key := strings.ToUpper(mop.Method) + " " + mop.Path
		op, ok := specOps[key]
		if !ok {
			v.errf("%s: manifest entry has no matching operation in the spec", key)
			continue
		}
		covered[key] = true
		if op.OperationID != "" && op.OperationID != mop.OperationID {
			v.errf("%s: manifest operationId %q != spec operationId %q", key, mop.OperationID, op.OperationID)
		}

		if mop.Request != nil {
			v.checkRequest(key, op, mop.Request.File, mop.Request.Kind)
		}
		seen := map[string]bool{}
		for _, r := range mop.Responses {
			seen[r.Status] = true
			v.checkResponse(key, op, r.Status, r.File, r.Kind)
		}
		for status := range op.Responses.Map() {
			if !seen[status] {
				v.errf("%s: spec declares response %s but no fixture covers it", key, status)
			}
		}
	}

	// Fixtures must exist for every endpoint (08-ROADMAP.md, milestone M0).
	for key := range specOps {
		if !covered[key] {
			v.errf("%s: operation has no fixture coverage in manifest.json", key)
		}
	}

	sort.Strings(v.errs)
	rep.violations = v.errs
	if len(rep.violations) == 0 {
		rep.ok = append(rep.ok, fmt.Sprintf("ok   %d operations, every request/response fixture validates against %s",
			len(specOps), filepath.Base(specPath)))
	}
	return rep, nil
}

type validator struct {
	fixturesDir string
	errs        []string
}

func (v *validator) errf(format string, a ...any) { v.errs = append(v.errs, fmt.Sprintf(format, a...)) }

func (v *validator) load(file string) (any, bool) {
	b, err := os.ReadFile(filepath.Join(v.fixturesDir, file))
	if err != nil {
		v.errf("fixture %s: %v", file, err)
		return nil, false
	}
	var val any
	if err := json.Unmarshal(b, &val); err != nil {
		v.errf("fixture %s: not valid JSON: %v", file, err)
		return nil, false
	}
	return val, true
}

func (v *validator) checkRequest(key string, op *openapi3.Operation, file, kind string) {
	if op.RequestBody == nil || op.RequestBody.Value == nil {
		v.errf("%s: manifest supplies a request fixture but the spec declares no request body", key)
		return
	}
	val, ok := v.load(file)
	if !ok {
		return
	}
	if kind == "headers" {
		return // binary body; the fixture only pins framing headers
	}
	mt := op.RequestBody.Value.Content.Get("application/json")
	if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
		v.errf("%s: request fixture %s is kind:json but the spec has no application/json request schema", key, file)
		return
	}
	if err := mt.Schema.Value.VisitJSON(val); err != nil {
		v.errf("%s request %s: %v", key, file, err)
	}
}

func (v *validator) checkResponse(key string, op *openapi3.Operation, status, file, kind string) {
	respRef := op.Responses.Map()[status]
	if respRef == nil {
		respRef = op.Responses.Map()["default"]
	}
	if respRef == nil || respRef.Value == nil {
		v.errf("%s: manifest covers response %s but the spec does not declare it", key, status)
		return
	}
	val, ok := v.load(file)
	if !ok {
		return
	}
	if kind == "headers" {
		return // 204 / binary body; fixture pins headers only
	}
	mt := respRef.Value.Content.Get("application/json")
	if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
		v.errf("%s: response fixture %s is kind:json but response %s has no application/json schema", key, file, status)
		return
	}
	if err := mt.Schema.Value.VisitJSON(val); err != nil {
		v.errf("%s response %s (%s): %v", key, status, file, err)
	}
}

func loadManifest(path string) (*manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}
