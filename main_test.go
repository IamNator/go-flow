package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fullstorydev/grpcurl"
	"google.golang.org/grpc/codes"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func writeFlowFile(t *testing.T, dir, name string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("vars:\n  base: http://example.com"), filePermission); err != nil {
		t.Fatalf("write flow file %q: %v", path, err)
	}

	return path
}

func TestResolveExportFilePath(t *testing.T) {
	t.Run("explicit file path", func(t *testing.T) {
		dir := t.TempDir()
		explicit := filepath.Join(dir, "exports", "result.json")

		got, err := resolveExportFilePath(explicit)
		if err != nil {
			t.Fatalf("resolveExportFilePath explicit: %v", err)
		}
		if got != explicit {
			t.Fatalf("expected %q, got %q", explicit, got)
		}
	})

	t.Run("directory path", func(t *testing.T) {
		dir := t.TempDir()
		exportDir := filepath.Join(dir, "exports")

		got, err := resolveExportFilePath(exportDir)
		if err != nil {
			t.Fatalf("resolveExportFilePath directory: %v", err)
		}

		if !strings.HasPrefix(got, exportDir) {
			t.Fatalf("expected result to be within %q, got %q", exportDir, got)
		}
		if filepath.Ext(got) != ".json" {
			t.Fatalf("expected json export file, got %q", got)
		}
	})
}

func TestResolveFlowTargets(t *testing.T) {
	dir := t.TempDir()

	fileA := writeFlowFile(t, dir, "002_alpha.yaml")
	writeFlowFile(t, dir, "001_beta.yml")

	t.Run("explicit file path", func(t *testing.T) {
		targets, err := resolveFlowTargets(fileA, "", "")
		if err != nil {
			t.Fatalf("resolveFlowTargets explicit: %v", err)
		}
		if len(targets) != 1 {
			t.Fatalf("expected 1 target, got %d", len(targets))
		}
		if targets[0].Name != "002_alpha" || targets[0].Path != fileA {
			t.Fatalf("unexpected target: %+v", targets[0])
		}
	})

	t.Run("all flows in dir", func(t *testing.T) {
		targets, err := resolveFlowTargets("", dir, "")
		if err != nil {
			t.Fatalf("resolveFlowTargets dir: %v", err)
		}
		if len(targets) != 2 {
			t.Fatalf("expected 2 targets, got %d", len(targets))
		}
		if targets[0].Name != "001_beta" || targets[1].Name != "002_alpha" {
			t.Fatalf("unexpected ordering: %+v", targets)
		}
	})

	t.Run("specific flow", func(t *testing.T) {
		targets, err := resolveFlowTargets("", dir, "002_alpha")
		if err != nil {
			t.Fatalf("resolveFlowTargets flow: %v", err)
		}
		if len(targets) != 1 || targets[0].Name != "002_alpha" {
			t.Fatalf("expected 002_alpha, got %+v", targets)
		}
	})

	t.Run("missing flow errors", func(t *testing.T) {
		if _, err := resolveFlowTargets("", dir, "does-not-exist"); err == nil {
			t.Fatalf("expected error for unknown flow")
		}
	})
}

func TestParseVarOverrides(t *testing.T) {
	overrides, err := parseVarOverrides([]string{"foo=bar", " spaced = value "})
	if err != nil {
		t.Fatalf("parseVarOverrides: %v", err)
	}

	if overrides["foo"] != "bar" {
		t.Fatalf("expected foo=bar, got %q", overrides["foo"])
	}
	if overrides["spaced"] != " value " {
		t.Fatalf("expected spaced to keep value spacing, got %q", overrides["spaced"])
	}

	if _, err := parseVarOverrides([]string{"invalid"}); err == nil {
		t.Fatalf("expected error for invalid override")
	}
}

func TestRunFlowAppliesOverrides(t *testing.T) {
	flowYAML := `vars:
  base_url: http://example.invalid
  payload: default-body
steps:
  - name: override-check
    method: POST
    url: "{{.base_url}}/echo"
    body: "{{.payload}}"
    expect_status: 200
`

	flowFile := filepath.Join(t.TempDir(), "override.yaml")
	if err := os.WriteFile(flowFile, []byte(flowYAML), filePermission); err != nil {
		t.Fatalf("write flow file: %v", err)
	}

	var (
		gotBody   string
		gotURL    string
		reqErrors []error
		callCount int
	)

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			callCount++
			body, err := io.ReadAll(req.Body)
			if err != nil {
				reqErrors = append(reqErrors, err)
				return nil, err
			}
			req.Body.Close()

			gotBody = string(body)
			gotURL = req.URL.String()

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	runner := &FlowRunner{
		client: client,
	}

	overrideURL := "http://override.test"
	overrides := map[string]string{
		"base_url": overrideURL,
		"payload":  "override-body",
	}

	if err := runner.RunFlow(context.Background(), flowFile, overrides); err != nil {
		t.Fatalf("RunFlow: %v", err)
	}

	if len(reqErrors) > 0 {
		t.Fatalf("request errors: %v", reqErrors)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 request, got %d", callCount)
	}
	if gotURL != overrideURL+"/echo" {
		t.Fatalf("expected URL %s/echo, got %q", overrideURL, gotURL)
	}

	if gotBody != overrides["payload"] {
		t.Fatalf("expected override body %q, got %q", overrides["payload"], gotBody)
	}
}

func TestRenderStringSlice(t *testing.T) {
	vars := map[string]string{"env": "dev", "service": "billing"}
	values := []string{" {{.env}}/health ", "", "grpc://{{.service}} "}

	got := renderStringSlice(values, vars)
	want := []string{"dev/health", "grpc://billing"}

	if len(got) != len(want) {
		t.Fatalf("expected %d values, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func TestParseGRPCCode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    codes.Code
		wantErr bool
	}{
		{"default OK", "", codes.OK, false},
		{"named code", "canceled", codes.Canceled, false},
		{"numeric code", "5", codes.Code(5), false},
		{"invalid", "definitely-bad", codes.OK, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGRPCCode(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGRPCCode: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestParseGRPCFormat(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    grpcurl.Format
		wantErr bool
	}{
		{"default json", "", grpcurl.FormatJSON, false},
		{"explicit json", "json", grpcurl.FormatJSON, false},
		{"text proto", "text", grpcurl.FormatText, false},
		{"invalid", "xml", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGRPCFormat(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGRPCFormat: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestBuildGRPCHeaders(t *testing.T) {
	vars := map[string]string{"token": "abc123", "customer": "enterprise"}
	headers := buildGRPCHeaders(map[string]string{
		"authorization": "Bearer {{.token}}",
		"x-customer":    "{{toUpper .customer}}",
		"  ":            "ignored",
	}, vars)

	want := []string{
		"authorization: Bearer abc123",
		"x-customer: ENTERPRISE",
	}

	if len(headers) != len(want) {
		t.Fatalf("expected %d headers, got %d (%v)", len(want), len(headers), headers)
	}
	for i := range want {
		if headers[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, headers)
		}
	}
}

func TestTrimLongString(t *testing.T) {
	long := strings.Repeat("x", maxDisplayedStringLen+10)
	trimmed := trimLongString(long)

	if len(trimmed) != maxDisplayedStringLen+3 {
		t.Fatalf("expected trimmed string length %d, got %d", maxDisplayedStringLen+3, len(trimmed))
	}
	if !strings.HasSuffix(trimmed, "...") {
		t.Fatalf("expected suffix ..., got %q", trimmed)
	}

	short := "short"
	if trimLongString(short) != short {
		t.Fatalf("short string should remain unchanged")
	}
}

func TestEnsureExpectedAffectedRows(t *testing.T) {
	step := Step{Name: "users", ExpectAffectedRows: 2}
	if err := ensureExpectedAffectedRows(step, 2); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	if err := ensureExpectedAffectedRows(step, 1); err == nil {
		t.Fatalf("expected mismatch error")
	}

	step.ExpectAffectedRows = 0
	if err := ensureExpectedAffectedRows(step, 5); err != nil {
		t.Fatalf("expect zero should ignore, got %v", err)
	}
}

func TestRender(t *testing.T) {
	vars := map[string]string{"name": "codex", "count": "5"}
	out := render(" Hello {{toUpper .name}} {{.count}} ", vars)

	if out != "Hello CODEX 5" {
		t.Fatalf("expected trimmed rendered string, got %q", out)
	}
}

func TestSaveValues(t *testing.T) {
	resp := []byte(`{"user":{"id":"123","email":"codex@example.com"}}`)
	saveMap := map[string]string{
		"user_id":    "user.id",
		"user_email": "user.email",
		"missing":    "missing.value",
	}
	vars := map[string]string{}

	saveValues(resp, saveMap, vars)

	if vars["user_id"] != "123" {
		t.Fatalf("expected user_id=123, got %q", vars["user_id"])
	}
	if vars["user_email"] != "codex@example.com" {
		t.Fatalf("expected email saved, got %q", vars["user_email"])
	}
	if _, ok := vars["missing"]; ok {
		t.Fatalf("expected missing path to be ignored")
	}
}

func TestVarExporterSkipsFileWithoutRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exports", "vars.json")

	exporter, err := newVarExporter(path)
	if err != nil {
		t.Fatalf("newVarExporter: %v", err)
	}

	if err := exporter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no file to be created, got err=%v", err)
	}

	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected directory to remain absent, got err=%v", err)
	}
}

func TestVarExporterWritesRecordsOnClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exports", "vars.json")

	exporter, err := newVarExporter(path)
	if err != nil {
		t.Fatalf("newVarExporter: %v", err)
	}

	exporter.Record("example", map[string]any{
		"user_id": "123",
	})

	if err := exporter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("expected directory to be created, got %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export file: %v", err)
	}

	var records []exportRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("unmarshal export json: %v", err)
	}

	if len(records) != 1 || records[0].Step != "example" {
		t.Fatalf("unexpected records: %+v", records)
	}
	if records[0].Vars["user_id"] != "123" {
		t.Fatalf("expected saved user_id 123, got %v", records[0].Vars["user_id"])
	}
}

func TestValidateAndSaveJSONHandlesBOM(t *testing.T) {
	step := Step{
		Name: "bom-step",
		Save: map[string]string{
			"foo": "value",
		},
	}
	payload := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"value":"123"}`)...)
	vars := map[string]string{}

	if err := validateAndSaveJSON(step, payload, vars, "response"); err != nil {
		t.Fatalf("validateAndSaveJSON: %v", err)
	}

	if vars["foo"] != "123" {
		t.Fatalf("expected foo=123, got %q", vars["foo"])
	}
}

func TestValidateAndSaveJSONInvalidPayload(t *testing.T) {
	step := Step{
		Name: "bad-json",
		Save: map[string]string{
			"foo": "value",
		},
	}
	payload := []byte{0xEF, 0xBB, 0xBF, 'n', 'o', 'p'}

	if err := validateAndSaveJSON(step, payload, map[string]string{}, "response"); err == nil {
		t.Fatalf("expected error for invalid JSON payload")
	}
}
