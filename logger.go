package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"
)

type stepLogContext struct {
	Request  map[string]any
	Response map[string]any
}

type logContextKey struct{}

type stepLogEntry struct {
	Step           string         `json:"step"`
	Type           string         `json:"type"`
	Status         string         `json:"status"`
	StartedAt      time.Time      `json:"started_at"`
	DurationMillis int64          `json:"duration_ms"`
	Request        map[string]any `json:"request,omitempty"`
	Response       map[string]any `json:"response,omitempty"`
	Error          string         `json:"error,omitempty"`
}

type runLogger struct {
	dir       string
	runID     string
	startedAt time.Time
	entries   []stepLogEntry
}

func newRunLogger(dir string) (*runLogger, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return nil, nil
	}

	return &runLogger{
		dir:       trimmed,
		runID:     time.Now().UTC().Format("20060102-150405"),
		startedAt: time.Now().UTC(),
		entries:   make([]stepLogEntry, 0),
	}, nil
}

func (l *runLogger) Record(entry stepLogEntry) {
	if l == nil {
		return
	}
	l.entries = append(l.entries, entry)
}

func (l *runLogger) Close() error {
	if l == nil || len(l.entries) == 0 {
		return nil
	}

	if err := ensureDirExists(l.dir); err != nil {
		return fmt.Errorf("create log directory %q: %w", l.dir, err)
	}

	jsonPath := filepath.Join(l.dir, fmt.Sprintf("%s.json", l.runID))
	htmlPath := filepath.Join(l.dir, fmt.Sprintf("%s.html", l.runID))

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(l.entries); err != nil {
		return fmt.Errorf("encode log entries: %w", err)
	}

	if err := os.WriteFile(jsonPath, buf.Bytes(), filePermission); err != nil {
		return fmt.Errorf("write log json: %w", err)
	}

	htmlContent := buildLogHTML(l.runID, buf.String())
	if err := os.WriteFile(htmlPath, []byte(htmlContent), filePermission); err != nil {
		return fmt.Errorf("write log html: %w", err)
	}

	fmt.Printf("%sLogs saved to %s and %s%s\n", colorCyan, jsonPath, htmlPath, colorReset)
	if link := formatFileURL(htmlPath); link != "" {
		fmt.Printf("   %sopen:%s %s%s%s\n", colorGray, colorReset, colorBlue, link, colorReset)
	}

	if err := openInBrowser(htmlPath); err != nil {
		fmt.Printf("%s⚠ unable to open log in browser: %v%s\n", colorRed, err, colorReset)
	}

	return nil
}

//go:embed log_tmpl.html
var logHTMLTemplate string

func buildLogHTML(runID string, jsonData string) string {
	safeData := sanitizeJSONForHTML(jsonData)

	tmpl := template.Must(template.New("log").Parse(logHTMLTemplate))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{
		"run_id":    runID,
		"json_data": safeData,
	}); err != nil {
		return fmt.Sprintf("failed to render log template: %v", err)
	}
	return buf.String()
}

func (c *stepLogContext) ensureRequestMap() map[string]any {
	if c == nil {
		return nil
	}
	if c.Request == nil {
		c.Request = make(map[string]any)
	}
	return c.Request
}

func (c *stepLogContext) ensureResponseMap() map[string]any {
	if c == nil {
		return nil
	}
	if c.Response == nil {
		c.Response = make(map[string]any)
	}
	return c.Response
}

func (r *FlowRunner) newStepLogContext() *stepLogContext {
	if r == nil || r.logger == nil {
		return nil
	}
	return &stepLogContext{}
}

func classifyStep(step Step) string {
	switch {
	case strings.TrimSpace(step.SQL) != "":
		return "sql"
	case step.Mongo != nil:
		return "mongo"
	case step.GRPC != nil:
		return "grpc"
	case step.Method != "" && step.URL != "":
		return "http"
	default:
		return "step"
	}
}

func flattenHTTPHeader(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}

	out := make(map[string]string, len(h))
	for k, vals := range h {
		out[k] = strings.Join(vals, ", ")
	}
	return out
}

type loggingTransport struct {
	base http.RoundTripper
}

func (t loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	startedAt := time.Now()
	resp, err := base.RoundTrip(req)

	if ctx := logContextFromRequest(req); ctx != nil {
		ctx.recordHTTPMetrics(req, resp, startedAt, time.Since(startedAt), err)
	}

	return resp, err
}

func logContextFromRequest(req *http.Request) *stepLogContext {
	if req == nil {
		return nil
	}

	if ctx, ok := req.Context().Value(logContextKey{}).(*stepLogContext); ok {
		return ctx
	}

	return nil
}

func (c *stepLogContext) recordHTTPMetrics(req *http.Request, resp *http.Response, startedAt time.Time, duration time.Duration, reqErr error) {
	if c == nil {
		return
	}

	if req != nil {
		reqMap := c.ensureRequestMap()
		if reqMap["method"] == nil {
			reqMap["method"] = req.Method
		}
		if req.URL != nil && reqMap["url"] == nil {
			reqMap["url"] = req.URL.String()
		}
		reqMap["headers"] = flattenHTTPHeader(req.Header)
		reqMap["sent_at"] = startedAt.UTC().Format(time.RFC3339Nano)
	}

	respMap := c.ensureResponseMap()
	respMap["duration_ms"] = duration.Milliseconds()

	if resp != nil {
		respMap["status"] = resp.StatusCode
		respMap["headers"] = flattenHTTPHeader(resp.Header)
	}

	if reqErr != nil {
		respMap["transport_error"] = reqErr.Error()
	}
}

func sanitizeJSONForHTML(data string) string {
	trimmed := strings.TrimSpace(data)
	return strings.ReplaceAll(trimmed, "</script>", "<\\/script>")
}

func normalizeJSONValue(text string) any {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	if json.Valid([]byte(trimmed)) {
		var v any
		if err := json.Unmarshal([]byte(trimmed), &v); err == nil {
			return v
		}
	}

	return text
}

func normalizeJSONBytes(data []byte) any {
	if len(data) == 0 {
		return ""
	}
	return normalizeJSONValue(string(data))
}

func openInBrowser(path string) error {
	if path == "" {
		return nil
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}

	if cmd == nil {
		return fmt.Errorf("unsupported platform for auto-opening logs")
	}

	return cmd.Start()
}

func formatFileURL(path string) string {
	if path == "" {
		return ""
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	slash := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}

	return "file://" + slash
}
