package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	legacyproto "github.com/golang/protobuf/proto"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"

	"github.com/fullstorydev/grpcurl"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/grpcreflect"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	_ "github.com/lib/pq"
)

const (
	defaultFlowDir      = "flow"
	dirPermission       = 0o755
	filePermission      = 0o644
	flowPrefixMinLength = 4
	flowNumberIncrement = 2
	httpClientTimeout   = 10 * time.Second
)

const (
	colorReset = "\033[0m"
	colorRed   = "\033[31m"
	colorGreen = "\033[32m"
	colorBlue  = "\033[34m"
	colorCyan  = "\033[36m"
	colorGray  = "\033[90m"
	bold       = "\033[1m"
)

type Flow struct {
	Vars  map[string]string `yaml:"vars"`
	Steps []Step            `yaml:"steps"`
}

type Step struct {
	Wait               string            `yaml:"wait"`
	Skip               bool              `yaml:"skip"`
	Export             bool              `yaml:"export"`
	Name               string            `yaml:"name"`
	TimeoutSeconds     int               `yaml:"timeout_seconds"`
	Method             string            `yaml:"method"`
	URL                string            `yaml:"url"`
	Headers            map[string]string `yaml:"headers"`
	Body               string            `yaml:"body"`
	ExpectStatus       int               `yaml:"expect_status"`
	Save               map[string]string `yaml:"save"` // key -> gjson path
	SQL                string            `yaml:"sql"`
	DatabaseURL        string            `yaml:"database_url"`
	ExpectAffectedRows int               `yaml:"expect_affected_rows"`
	Mongo              *MongoStep        `yaml:"mongo"`
	GRPC               *GRPCStep         `yaml:"grpc"`
}

type MongoStep struct {
	URI        string `yaml:"uri"`
	Database   string `yaml:"database"`
	Collection string `yaml:"collection"`
	Operation  string `yaml:"operation"`
	Filter     string `yaml:"filter"`
	Document   string `yaml:"document"`
	Update     string `yaml:"update"`
	Pipeline   string `yaml:"pipeline"`
	Command    string `yaml:"command"`
	Limit      int64  `yaml:"limit"`
}

type GRPCStep struct {
	Target             string            `yaml:"target"`
	Method             string            `yaml:"method"`
	Request            string            `yaml:"request"`
	Format             string            `yaml:"format"`
	Metadata           map[string]string `yaml:"metadata"`
	ReflectionMetadata map[string]string `yaml:"reflection_metadata"`
	UseTLS             bool              `yaml:"use_tls"`
	SkipTLSVerify      bool              `yaml:"skip_tls_verify"`
	CACert             string            `yaml:"ca_cert"`
	ClientCert         string            `yaml:"client_cert"`
	ClientKey          string            `yaml:"client_key"`
	ServerName         string            `yaml:"server_name"`
	ProtoSets          []string          `yaml:"proto_sets"`
	ProtoFiles         []string          `yaml:"proto_files"`
	ProtoPaths         []string          `yaml:"proto_paths"`
	UseReflection      *bool             `yaml:"use_reflection"`
	ExpectCode         string            `yaml:"expect_code"`
}

type FlowFile struct {
	Name string
	Path string
}

type FlowRunner struct {
	client   *http.Client
	exporter *varExporter
}

type exportRecord struct {
	Step string         `json:"step"`
	Vars map[string]any `json:"vars"`
}

type varExporter struct {
	file    *os.File
	records []exportRecord
}

func newFlowRunner(exportPath string) (*FlowRunner, error) {
	exporter, err := newVarExporter(exportPath)
	if err != nil {
		return nil, err
	}

	return &FlowRunner{
		client:   &http.Client{Timeout: httpClientTimeout},
		exporter: exporter,
	}, nil
}

func (r *FlowRunner) Close() error {
	if r == nil {
		return nil
	}

	if r.exporter == nil {
		return nil
	}

	return r.exporter.Close()
}

func newVarExporter(path string) (*varExporter, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create exported vars file: %w", err)
	}

	return &varExporter{
		file:    file,
		records: make([]exportRecord, 0),
	}, nil
}

func (e *varExporter) Record(stepName string, values map[string]any) {
	if e == nil {
		return
	}

	exportVars := make(map[string]any, len(values))
	maps.Copy(exportVars, values)

	e.records = append(e.records, exportRecord{
		Step: stepName,
		Vars: exportVars,
	})
}

func (e *varExporter) Close() error {
	if e == nil || e.file == nil {
		return nil
	}
	defer e.file.Close()

	encoder := json.NewEncoder(e.file)
	encoder.SetIndent("", "  ")

	records := e.records
	if records == nil {
		records = make([]exportRecord, 0)
	}

	if err := encoder.Encode(records); err != nil {
		return fmt.Errorf("write exported vars: %w", err)
	}

	return nil
}

func (r *FlowRunner) recordExport(step Step, vars map[string]string) {
	if !step.Export || r.exporter == nil {
		return
	}

	exportMap := make(map[string]any, len(step.Save))
	for k := range step.Save {
		exportMap[k] = vars[k]
	}

	r.exporter.Record(step.Name, exportMap)
}

func (s *Step) applyDefaults() {
	if s.TimeoutSeconds == 0 {
		s.TimeoutSeconds = 10
	}
}

func main() {
	app := &cli.App{
		Name:           "go-flow",
		Usage:          "Run flows defined in YAML files",
		DefaultCommand: "run",
		Commands: []*cli.Command{
			{
				Name:  "new",
				Usage: "Create a new flow file with a basic template",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "dir",
						Aliases: []string{"d"},
						Value:   defaultFlowDir,
						Usage:   "Directory to create the new flow file in",
					},
				},
				Action: func(c *cli.Context) error {
					flowName := c.Args().First()
					if flowName == "" {
						return errors.New("flow name is required")
					}

					dir := c.String("dir")
					if err := os.MkdirAll(dir, dirPermission); err != nil {
						return fmt.Errorf("create flow directory: %w", err)
					}

					// Find the highest existing prefix in the directory (e.g., 001_, 004_, etc.)
					files, err := os.ReadDir(dir)
					if err != nil {
						return fmt.Errorf("read flow directory: %w", err)
					}

					maxNum := 0
					for _, f := range files {
						name := f.Name()
						if len(name) < flowPrefixMinLength {
							continue
						}
						var num int
						if _, err := fmt.Sscanf(name, "%d_", &num); err == nil {
							if num > maxNum {
								maxNum = num
							}
						}
					}

					nextNum := maxNum + flowNumberIncrement
					filename := fmt.Sprintf("%03d_%s.yaml", nextNum, flowName)
					flowFilePath := filepath.Join(dir, filename)

					if _, err := os.Stat(flowFilePath); err == nil {
						return fmt.Errorf("flow file %q already exists", flowFilePath)
					}

					templateContent := `vars:
  base: http://localhost:8080/api/v1

steps:
  - name: example-step
    method: GET
    url: "{{.base}}/example"
    expect_status: 200
	save:
	  user_email: data.email

  - name: example-sql-step
	sql: |
	  SELECT id, name FROM users WHERE email = '{{.user_email}}';
	save:
	  user_id: id
	  user_name: name
`

					if err := os.WriteFile(flowFilePath, []byte(templateContent), filePermission); err != nil {
						return fmt.Errorf("write flow file %q: %w", flowFilePath, err)
					}

					fmt.Printf("✅ Created new flow file: %s\n", flowFilePath)
					return nil
				},
			},
			{
				Name:  "run",
				Usage: "Execute an HTTP flow",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "file",
						Aliases: []string{"f"},
						Usage:   "Explicit path to a flow file (overrides dir/flow)",
					},
					&cli.StringFlag{
						Name:    "dir",
						Aliases: []string{"d"},
						Value:   defaultFlowDir,
						Usage:   "Directory containing flow files",
					},
					&cli.StringFlag{
						Name:    "flow",
						Aliases: []string{"n"},
						Value:   "",
						Usage:   "Flow name (file name without extension) within dir",
					},
					&cli.StringSliceFlag{
						Name:    "var",
						Aliases: []string{"v"},
						Usage:   "Override flow variable (format key=value). Can be provided multiple times",
					},
					&cli.StringFlag{
						Name:    "export_file",
						Aliases: []string{"e"},
						Value:   "exported_vars.json",
						Usage:   "Path to export collected variables as JSON",
					},
				},
				Action: runFlowsAction,
			},
			{
				Name:  "list",
				Usage: "List available flows in a directory",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "dir",
						Aliases: []string{"d"},
						Value:   defaultFlowDir,
						Usage:   "Directory containing flow files",
					},
				},
				Action: func(c *cli.Context) error {
					flows, err := listFlows(c.String("dir"))
					if err != nil {
						return err
					}
					for _, flow := range flows {
						fmt.Println(flow.Name)
					}
					return nil
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func runFlowsAction(c *cli.Context) (err error) {
	targets, err := resolveFlowTargets(c.String("file"), c.String("dir"), c.String("flow"))
	if err != nil {
		return err
	}

	overrideVars, err := parseVarOverrides(c.StringSlice("var"))
	if err != nil {
		return err
	}

	if len(targets) == 0 {
		return errors.New("no flow files found")
	}

	exportFilePath := c.String("export_file")
	runner, err := newFlowRunner(exportFilePath)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := runner.Close()
		if err == nil {
			err = closeErr
		}
	}()

	for idx, target := range targets {
		if idx > 0 {
			fmt.Println()
		}

		fmt.Printf("%s=== Flow: %s (%s) ===%s\n", bold+colorCyan, target.Name, target.Path, colorReset)

		if err := runner.RunFlow(c.Context, target.Path, overrideVars); err != nil {
			return err
		}
	}

	return nil
}

func resolveFlowTargets(filePath, dir, flow string) ([]FlowFile, error) {
	if filePath != "" {
		info, err := os.Stat(filePath)
		if err != nil {
			return nil, fmt.Errorf("flow file %q not accessible: %w", filePath, err)
		}

		if info.IsDir() {
			return nil, fmt.Errorf("flow file %q is a directory", filePath)
		}

		name := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

		return []FlowFile{{Name: name, Path: filePath}}, nil
	}

	if dir == "" {
		dir = "."
	}

	flows, err := listFlows(dir)
	if err != nil {
		return nil, err
	}

	if flow == "" {
		if len(flows) == 0 {
			return nil, fmt.Errorf("no flow files found in %q", dir)
		}

		return flows, nil
	}

	normalized := flow
	if ext := filepath.Ext(normalized); ext != "" {
		normalized = strings.TrimSuffix(normalized, ext)
	}

	for _, f := range flows {
		if f.Name == normalized {
			return []FlowFile{f}, nil
		}
	}

	return nil, fmt.Errorf("flow %q not found in %q", flow, dir)
}

func listFlows(dir string) ([]FlowFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read flow directory: %w", err)
	}

	var flows []FlowFile

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		flows = append(flows, FlowFile{
			Name: strings.TrimSuffix(name, ext),
			Path: filepath.Join(dir, name),
		})
	}

	sort.Slice(flows, func(i, j int) bool {
		return flows[i].Name < flows[j].Name
	})

	return flows, nil
}

func (r *FlowRunner) RunFlow(ctx context.Context, flowPath string, overrides map[string]string) error {
	data, err := os.ReadFile(flowPath)
	if err != nil {
		return fmt.Errorf("read flow file: %w", err)
	}

	var flow Flow
	if err := yaml.Unmarshal(data, &flow); err != nil {
		return fmt.Errorf("parse flow file: %w", err)
	}

	vars := map[string]string{}
	if flow.Vars != nil {
		maps.Copy(vars, flow.Vars)
	}

	maps.Copy(vars, overrides)

	for _, step := range flow.Steps {
		if err := r.executeStep(ctx, step, vars); err != nil {
			return err
		}
	}

	return nil
}

func parseVarOverrides(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}

	overrides := make(map[string]string, len(pairs))

	const pathLen = 2

	for _, pair := range pairs {
		if pair == "" {
			continue
		}

		parts := strings.SplitN(pair, "=", pathLen)
		if len(parts) != pathLen {
			return nil, fmt.Errorf("invalid var override %q, expected key=value", pair)
		}

		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("invalid var override %q, empty key", pair)
		}

		overrides[key] = parts[1]
	}

	return overrides, nil
}

const (
	maxDisplayedStringLen = 120
)

func (r *FlowRunner) executeStep(ctx context.Context, step Step, vars map[string]string) error {
	if step.Skip {
		fmt.Printf("%s→ Skipping step %q%s\n", colorGray, step.Name, colorReset)
		return nil
	}

	if step.Wait != "" {
		timeToWait, err := time.ParseDuration(render(step.Wait, vars))
		if err != nil {
			return fmt.Errorf("parse wait duration for step %q: %w", step.Name, err)
		}

		fmt.Printf("%s→ Waiting %s before step %q%s\n", colorGray, timeToWait.String(), step.Name, colorReset)

		// printing remaining time every second
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		// listen for interruption signals
		signalChan := make(chan os.Signal, 1)
		signal.Notify(signalChan, os.Interrupt)

		done := make(chan struct{})
		go func() {
			time.Sleep(timeToWait)
			close(done)
		}()

		remaining := timeToWait
		moveOn := false
		for !moveOn {
			select {
			case <-done:
				fmt.Printf("%s→ Wait complete for step %q%s\n", colorGray, step.Name, colorReset)
				moveOn = true
			case <-ticker.C:
				remaining -= 1 * time.Second
				if remaining < 0 {
					remaining = 0
				}
				fmt.Printf(" %s→ Waiting... %s remaining for step %q%s\r", colorGray, remaining.String(), step.Name, colorReset)
			case <-signalChan:
				fmt.Printf("\n%s→ Wait interrupted for step %q%s\n", colorGray, step.Name, colorReset)
				close(done)
			}
		}
	}

	sqlStmt := strings.TrimSpace(render(step.SQL, vars))
	if sqlStmt != "" {
		step.applyDefaults()
		return executeSQLStep(ctx, step, sqlStmt, vars)
	}

	if step.Mongo != nil {
		step.applyDefaults()
		return r.executeMongoStep(ctx, step, vars)
	}

	if step.GRPC != nil {
		step.applyDefaults()
		return r.executeGRPCStep(ctx, step, vars)
	}

	if step.Method == "" || step.URL == "" {
		return fmt.Errorf("step %q requires sql, grpc, or method/url fields", step.Name)
	}

	url := render(step.URL, vars)
	bodyStr := render(step.Body, vars)

	var body io.Reader
	if bodyStr != "" {
		body = bytes.NewBufferString(bodyStr)
	}

	step.applyDefaults()

	stepCtx, cancel := context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(stepCtx, step.Method, url, body)
	if err != nil {
		return fmt.Errorf("build request for step %q: %w", step.Name, err)
	}

	for k, v := range step.Headers {
		req.Header.Set(k, render(v, vars))
	}

	fmt.Printf("%s⇒ %s%s %s %s%s\n",
		colorBlue,
		step.Name,
		colorReset,
		step.Method,
		trimLongString(url),
		colorReset,
	)

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request for step %q: %w", step.Name, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body for step %q: %w", step.Name, err)
	}

	if step.ExpectStatus != 0 && resp.StatusCode != step.ExpectStatus {
		fmt.Printf("%s✖ %s: expected %d, got %d%s\n",
			colorRed,
			step.Name,
			step.ExpectStatus,
			resp.StatusCode,
			colorReset,
		)

		fmt.Println(string(respBytes))

		return fmt.Errorf("step %q failed: unexpected status %d", step.Name, resp.StatusCode)
	}

	if len(step.Save) > 0 && len(respBytes) > 0 && json.Valid(respBytes) {
		saveValues(respBytes, step.Save, vars)
	}

	r.recordExport(step, vars)

	fmt.Printf("%s✓ %s%s\n", colorGreen, step.Name, colorReset)

	return nil
}

func (r *FlowRunner) executeMongoStep(ctx context.Context, step Step, vars map[string]string) error {
	cfg := step.Mongo
	if cfg == nil {
		return fmt.Errorf("step %q missing mongo configuration", step.Name)
	}

	uri := strings.TrimSpace(render(cfg.URI, vars))
	if uri == "" {
		uri = strings.TrimSpace(vars["mongo_uri"])
	}
	if uri == "" {
		uri = strings.TrimSpace(os.Getenv("MONGO_URI"))
	}
	if uri == "" {
		return fmt.Errorf("step %q requires mongo.uri (field, var mongo_uri, or MONGO_URI env)", step.Name)
	}

	dbName := strings.TrimSpace(render(cfg.Database, vars))
	if dbName == "" {
		dbName = strings.TrimSpace(vars["mongo_database"])
	}
	if dbName == "" {
		return fmt.Errorf("step %q requires mongo.database (field or mongo_database var)", step.Name)
	}

	op := normalizeMongoOperation(cfg.Operation)
	useCollection := op != mongoOpCommand

	var collName string
	if useCollection {
		collName = strings.TrimSpace(render(cfg.Collection, vars))
		if collName == "" {
			collName = strings.TrimSpace(vars["mongo_collection"])
		}
		if collName == "" {
			return fmt.Errorf("step %q requires mongo.collection for operation %q", step.Name, op)
		}
	}

	stepCtx, cancel := context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
	defer cancel()

	client, err := mongo.Connect(stepCtx, options.Client().ApplyURI(uri))
	if err != nil {
		return fmt.Errorf("connect mongo for step %q: %w", step.Name, err)
	}
	defer func() {
		disconnectCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Disconnect(disconnectCtx)
	}()

	if err := client.Ping(stepCtx, nil); err != nil {
		return fmt.Errorf("ping mongo for step %q: %w", step.Name, err)
	}

	db := client.Database(dbName)
	var collection *mongo.Collection
	if useCollection {
		collection = db.Collection(collName)
	}

	targetLabel := dbName
	if collName != "" {
		targetLabel = fmt.Sprintf("%s.%s", dbName, collName)
	}

	fmt.Printf("%s⇒ %s%s Mongo %s %s%s\n",
		colorBlue,
		step.Name,
		colorReset,
		strings.ToUpper(op),
		targetLabel,
		colorReset,
	)

	var resultPayload []byte
	affected := 0

	switch op {
	case mongoOpFindOne:
		filterDoc, err := parseBSONDocument(render(cfg.Filter, vars))
		if err != nil {
			return fmt.Errorf("step %q: parse mongo filter: %w", step.Name, err)
		}

		var doc bson.M
		err = collection.FindOne(stepCtx, filterDoc).Decode(&doc)
		if errors.Is(err, mongo.ErrNoDocuments) {
			resultPayload = []byte("null")
		} else if err != nil {
			return fmt.Errorf("step %q: mongo findOne failed: %w", step.Name, err)
		} else {
			affected = 1
			resultPayload, err = bsonToJSON(doc)
			if err != nil {
				return fmt.Errorf("step %q: encode mongo findOne result: %w", step.Name, err)
			}
		}
	case mongoOpFind:
		filterDoc, err := parseBSONDocument(render(cfg.Filter, vars))
		if err != nil {
			return fmt.Errorf("step %q: parse mongo filter: %w", step.Name, err)
		}

		findOpts := options.Find()
		if cfg.Limit > 0 {
			findOpts.SetLimit(cfg.Limit)
		}

		cursor, err := collection.Find(stepCtx, filterDoc, findOpts)
		if err != nil {
			return fmt.Errorf("step %q: mongo find failed: %w", step.Name, err)
		}
		defer cursor.Close(stepCtx)

		var docs []bson.M
		if err := cursor.All(stepCtx, &docs); err != nil {
			return fmt.Errorf("step %q: read mongo find results: %w", step.Name, err)
		}

		affected = len(docs)
		resultPayload, err = bsonToJSON(docs)
		if err != nil {
			return fmt.Errorf("step %q: encode mongo find results: %w", step.Name, err)
		}
	case mongoOpAggregate:
		pipeline, err := parseBSONArray(render(cfg.Pipeline, vars))
		if err != nil {
			return fmt.Errorf("step %q: parse mongo pipeline: %w", step.Name, err)
		}

		cursor, err := collection.Aggregate(stepCtx, pipeline)
		if err != nil {
			return fmt.Errorf("step %q: mongo aggregate failed: %w", step.Name, err)
		}
		defer cursor.Close(stepCtx)

		var docs []bson.M
		if err := cursor.All(stepCtx, &docs); err != nil {
			return fmt.Errorf("step %q: read mongo aggregate results: %w", step.Name, err)
		}

		affected = len(docs)
		resultPayload, err = bsonToJSON(docs)
		if err != nil {
			return fmt.Errorf("step %q: encode mongo aggregate results: %w", step.Name, err)
		}
	case mongoOpInsertOne:
		document, err := parseBSONDocument(render(cfg.Document, vars))
		if err != nil {
			return fmt.Errorf("step %q: parse mongo document: %w", step.Name, err)
		}
		if len(document) == 0 {
			return fmt.Errorf("step %q: mongo document is required for insertOne", step.Name)
		}

		res, err := collection.InsertOne(stepCtx, document)
		if err != nil {
			return fmt.Errorf("step %q: mongo insertOne failed: %w", step.Name, err)
		}

		affected = 1
		resultPayload, err = bsonToJSON(bson.M{"inserted_id": res.InsertedID})
		if err != nil {
			return fmt.Errorf("step %q: encode mongo insertOne result: %w", step.Name, err)
		}
	case mongoOpUpdateOne:
		filterDoc, err := parseBSONDocument(render(cfg.Filter, vars))
		if err != nil {
			return fmt.Errorf("step %q: parse mongo filter: %w", step.Name, err)
		}
		updateDoc, err := parseBSONDocument(render(cfg.Update, vars))
		if err != nil {
			return fmt.Errorf("step %q: parse mongo update: %w", step.Name, err)
		}
		if len(updateDoc) == 0 {
			return fmt.Errorf("step %q: mongo update document is required for updateOne", step.Name)
		}

		res, err := collection.UpdateOne(stepCtx, filterDoc, updateDoc)
		if err != nil {
			return fmt.Errorf("step %q: mongo updateOne failed: %w", step.Name, err)
		}

		affected = int(res.ModifiedCount)
		if affected == 0 && res.UpsertedCount > 0 {
			affected = int(res.UpsertedCount)
		}

		payload := bson.M{
			"matched_count":  res.MatchedCount,
			"modified_count": res.ModifiedCount,
			"upserted_count": res.UpsertedCount,
		}
		if res.UpsertedID != nil {
			payload["upserted_id"] = res.UpsertedID
		}

		resultPayload, err = bsonToJSON(payload)
		if err != nil {
			return fmt.Errorf("step %q: encode mongo updateOne result: %w", step.Name, err)
		}
	case mongoOpDeleteOne:
		filterDoc, err := parseBSONDocument(render(cfg.Filter, vars))
		if err != nil {
			return fmt.Errorf("step %q: parse mongo filter: %w", step.Name, err)
		}

		res, err := collection.DeleteOne(stepCtx, filterDoc)
		if err != nil {
			return fmt.Errorf("step %q: mongo deleteOne failed: %w", step.Name, err)
		}

		affected = int(res.DeletedCount)

		resultPayload, err = bsonToJSON(bson.M{"deleted_count": res.DeletedCount})
		if err != nil {
			return fmt.Errorf("step %q: encode mongo deleteOne result: %w", step.Name, err)
		}
	case mongoOpCommand:
		cmdStr := strings.TrimSpace(render(cfg.Command, vars))
		if cmdStr == "" {
			return fmt.Errorf("step %q: mongo command payload is required", step.Name)
		}

		commandDoc, err := parseBSONDocument(cmdStr)
		if err != nil {
			return fmt.Errorf("step %q: parse mongo command: %w", step.Name, err)
		}

		var result bson.M
		if err := db.RunCommand(stepCtx, commandDoc).Decode(&result); err != nil {
			return fmt.Errorf("step %q: mongo command failed: %w", step.Name, err)
		}

		affected = 1
		resultPayload, err = bsonToJSON(result)
		if err != nil {
			return fmt.Errorf("step %q: encode mongo command result: %w", step.Name, err)
		}
	default:
		return fmt.Errorf("step %q: unsupported mongo operation %q", step.Name, cfg.Operation)
	}

	if err := ensureExpectedAffectedRows(step, affected); err != nil {
		return err
	}

	if len(step.Save) > 0 && len(resultPayload) > 0 && json.Valid(resultPayload) {
		saveValues(resultPayload, step.Save, vars)
	}

	r.recordExport(step, vars)

	fmt.Printf("%s✓ %s%s\n", colorGreen, step.Name, colorReset)

	return nil
}

const (
	mongoOpFindOne   = "findone"
	mongoOpFind      = "find"
	mongoOpAggregate = "aggregate"
	mongoOpInsertOne = "insertone"
	mongoOpUpdateOne = "updateone"
	mongoOpDeleteOne = "deleteone"
	mongoOpCommand   = "command"
)

func normalizeMongoOperation(op string) string {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "", "findone", "find_one":
		return mongoOpFindOne
	case "find", "findmany", "find_many":
		return mongoOpFind
	case "aggregate":
		return mongoOpAggregate
	case "insert", "insertone", "insert_one":
		return mongoOpInsertOne
	case "update", "updateone", "update_one":
		return mongoOpUpdateOne
	case "delete", "deleteone", "delete_one":
		return mongoOpDeleteOne
	case "command":
		return mongoOpCommand
	default:
		return strings.ToLower(strings.TrimSpace(op))
	}
}

func parseBSONDocument(payload string) (bson.M, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return bson.M{}, nil
	}

	var doc bson.M
	if err := bson.UnmarshalExtJSON([]byte(trimmed), true, &doc); err != nil {
		return nil, err
	}

	return doc, nil
}

func parseBSONArray(payload string) (bson.A, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("pipeline is required")
	}

	var arr bson.A
	if err := bson.UnmarshalExtJSON([]byte(trimmed), true, &arr); err != nil {
		return nil, err
	}

	return arr, nil
}

func bsonToJSON(value any) ([]byte, error) {
	return bson.MarshalExtJSON(value, true, true)
}

func (r *FlowRunner) executeGRPCStep(ctx context.Context, step Step, vars map[string]string) error {
	cfg := step.GRPC
	if cfg == nil {
		return fmt.Errorf("step %q missing grpc configuration", step.Name)
	}

	target := strings.TrimSpace(render(cfg.Target, vars))
	if target == "" {
		return fmt.Errorf("step %q requires grpc.target", step.Name)
	}

	method := strings.TrimSpace(render(cfg.Method, vars))
	if method == "" {
		return fmt.Errorf("step %q requires grpc.method", step.Name)
	}

	format, err := parseGRPCFormat(cfg.Format)
	if err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}

	payload := render(cfg.Request, vars)
	headers := buildGRPCHeaders(cfg.Metadata, vars)
	reflectionHeaders := buildGRPCHeaders(cfg.ReflectionMetadata, vars)

	fmt.Printf("%s⇒ %s%s gRPC %s %s%s\n",
		colorBlue,
		step.Name,
		colorReset,
		method,
		trimLongString(target),
		colorReset,
	)

	stepCtx, cancel := context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
	defer cancel()

	conn, err := dialGRPC(stepCtx, target, cfg, vars)
	if err != nil {
		return fmt.Errorf("dial grpc for step %q: %w", step.Name, err)
	}
	defer conn.Close()

	descSource, cleanup, err := buildDescriptorSource(stepCtx, conn, cfg, vars, reflectionHeaders)
	if err != nil {
		return fmt.Errorf("prepare descriptor source for step %q: %w", step.Name, err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	parserInput := strings.NewReader(payload)
	parser, formatter, err := grpcurl.RequestParserAndFormatter(format, descSource, parserInput, grpcurl.FormatOptions{
		EmitJSONDefaultFields: true,
	})
	if err != nil {
		return fmt.Errorf("build grpc request parser for step %q: %w", step.Name, err)
	}

	handler := &grpcCaptureEventHandler{formatter: formatter}
	if err := grpcurl.InvokeRPC(stepCtx, descSource, conn, method, headers, handler, parser.Next); err != nil {
		return fmt.Errorf("grpc call for step %q: %w", step.Name, err)
	}

	if err := handler.Error(); err != nil {
		return fmt.Errorf("process grpc response for step %q: %w", step.Name, err)
	}

	respStatus := handler.Status()
	if respStatus == nil {
		respStatus = status.New(codes.OK, "")
	}

	expectedCode := strings.TrimSpace(cfg.ExpectCode)
	if expectedCode != "" {
		code, err := parseGRPCCode(expectedCode)
		if err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
		if respStatus.Code() != code {
			return fmt.Errorf("step %q failed: expected %s but got %s (%s)",
				step.Name,
				code.String(),
				respStatus.Code().String(),
				respStatus.Message(),
			)
		}
	} else if respStatus.Code() != codes.OK {
		return fmt.Errorf("step %q failed: %s", step.Name, respStatus.String())
	}

	respBytes := handler.ResponsePayload()
	if len(step.Save) > 0 && len(respBytes) > 0 && json.Valid(respBytes) {
		saveValues(respBytes, step.Save, vars)
	}

	r.recordExport(step, vars)

	fmt.Printf("%s✓ %s%s\n", colorGreen, step.Name, colorReset)

	return nil
}

func parseGRPCFormat(value string) (grpcurl.Format, error) {
	format := strings.ToLower(strings.TrimSpace(value))
	switch format {
	case "", "json":
		return grpcurl.FormatJSON, nil
	case "text", "proto", "protobuf":
		return grpcurl.FormatText, nil
	default:
		return "", fmt.Errorf("unsupported grpc format %q", value)
	}
}

func buildGRPCHeaders(values map[string]string, vars map[string]string) []string {
	if len(values) == 0 {
		return nil
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		keys = append(keys, trimmed)
	}

	sort.Strings(keys)

	headers := make([]string, 0, len(keys))
	for _, key := range keys {
		headers = append(headers, fmt.Sprintf("%s: %s", key, render(values[key], vars)))
	}

	return headers
}

func dialGRPC(ctx context.Context, target string, cfg *GRPCStep, vars map[string]string) (*grpc.ClientConn, error) {
	creds, err := transportCredentialsForStep(cfg, vars)
	if err != nil {
		return nil, err
	}

	return grpc.DialContext(
		ctx,
		target,
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
	)
}

func transportCredentialsForStep(cfg *GRPCStep, vars map[string]string) (credentials.TransportCredentials, error) {
	if cfg == nil || !cfg.UseTLS {
		return insecure.NewCredentials(), nil
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if cfg.SkipTLSVerify {
		tlsConfig.InsecureSkipVerify = true
	}

	if serverName := strings.TrimSpace(render(cfg.ServerName, vars)); serverName != "" {
		tlsConfig.ServerName = serverName
	}

	if caPath := strings.TrimSpace(render(cfg.CACert, vars)); caPath != "" {
		caBytes, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read grpc ca_cert %q: %w", caPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return nil, fmt.Errorf("grpc ca_cert %q contains no valid certificates", caPath)
		}
		tlsConfig.RootCAs = pool
	}

	clientCertPath := strings.TrimSpace(render(cfg.ClientCert, vars))
	clientKeyPath := strings.TrimSpace(render(cfg.ClientKey, vars))
	if clientCertPath != "" || clientKeyPath != "" {
		if clientCertPath == "" || clientKeyPath == "" {
			return nil, errors.New("grpc client_cert and client_key must both be provided")
		}
		cert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
		if err != nil {
			return nil, fmt.Errorf("load grpc client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return credentials.NewTLS(tlsConfig), nil
}

func buildDescriptorSource(
	ctx context.Context,
	conn *grpc.ClientConn,
	cfg *GRPCStep,
	vars map[string]string,
	reflectionHeaders []string,
) (grpcurl.DescriptorSource, func(), error) {
	fileSource, err := loadFileDescriptorSource(cfg, vars)
	if err != nil {
		return nil, nil, err
	}

	var cleanup func()
	var descriptor grpcurl.DescriptorSource

	if boolValue(cfg.UseReflection, true) {
		refCtx := ctx
		if len(reflectionHeaders) > 0 {
			md := grpcurl.MetadataFromHeaders(reflectionHeaders)
			refCtx = metadata.NewOutgoingContext(refCtx, md)
		}

		refClient := grpcreflect.NewClientAuto(refCtx, conn)
		cleanup = func() {
			refClient.Reset()
		}

		reflectionSource := grpcurl.DescriptorSourceFromServer(ctx, refClient)
		if fileSource != nil {
			descriptor = compositeDescriptorSource{
				reflection: reflectionSource,
				file:       fileSource,
			}
		} else {
			descriptor = reflectionSource
		}
	} else if fileSource != nil {
		descriptor = fileSource
	}

	if descriptor == nil {
		return nil, nil, errors.New("grpc step requires reflection (use_reflection) or proto descriptors")
	}

	return descriptor, cleanup, nil
}

func loadFileDescriptorSource(cfg *GRPCStep, vars map[string]string) (grpcurl.DescriptorSource, error) {
	if cfg == nil {
		return nil, nil
	}

	protoSets := renderStringSlice(cfg.ProtoSets, vars)
	protoFiles := renderStringSlice(cfg.ProtoFiles, vars)
	protoPaths := renderStringSlice(cfg.ProtoPaths, vars)

	if len(protoSets) > 0 && len(protoFiles) > 0 {
		return nil, errors.New("grpc step cannot set both proto_sets and proto_files")
	}

	if len(protoSets) > 0 {
		return grpcurl.DescriptorSourceFromProtoSets(protoSets...)
	}

	if len(protoFiles) > 0 {
		return grpcurl.DescriptorSourceFromProtoFiles(protoPaths, protoFiles...)
	}

	return nil, nil
}

func renderStringSlice(values []string, vars map[string]string) []string {
	if len(values) == 0 {
		return nil
	}

	output := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(render(value, vars))
		if trimmed == "" {
			continue
		}
		output = append(output, trimmed)
	}

	return output
}

func boolValue(value *bool, def bool) bool {
	if value == nil {
		return def
	}
	return *value
}

var grpcCodeLookup = map[string]codes.Code{
	"OK":                  codes.OK,
	"CANCELED":            codes.Canceled,
	"UNKNOWN":             codes.Unknown,
	"INVALID_ARGUMENT":    codes.InvalidArgument,
	"DEADLINE_EXCEEDED":   codes.DeadlineExceeded,
	"NOT_FOUND":           codes.NotFound,
	"ALREADY_EXISTS":      codes.AlreadyExists,
	"PERMISSION_DENIED":   codes.PermissionDenied,
	"RESOURCE_EXHAUSTED":  codes.ResourceExhausted,
	"FAILED_PRECONDITION": codes.FailedPrecondition,
	"ABORTED":             codes.Aborted,
	"OUT_OF_RANGE":        codes.OutOfRange,
	"UNIMPLEMENTED":       codes.Unimplemented,
	"INTERNAL":            codes.Internal,
	"UNAVAILABLE":         codes.Unavailable,
	"DATA_LOSS":           codes.DataLoss,
	"UNAUTHENTICATED":     codes.Unauthenticated,
}

func parseGRPCCode(value string) (codes.Code, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return codes.OK, nil
	}

	if code, ok := grpcCodeLookup[strings.ToUpper(v)]; ok {
		return code, nil
	}

	if num, err := strconv.Atoi(v); err == nil {
		return codes.Code(num), nil
	}

	return codes.OK, fmt.Errorf("unknown grpc expect_code %q", value)
}

type grpcCaptureEventHandler struct {
	formatter grpcurl.Formatter
	responses [][]byte
	status    *status.Status
	err       error
}

func (h *grpcCaptureEventHandler) OnResolveMethod(md *desc.MethodDescriptor) {}

func (h *grpcCaptureEventHandler) OnSendHeaders(md metadata.MD) {}

func (h *grpcCaptureEventHandler) OnReceiveHeaders(md metadata.MD) {}

func (h *grpcCaptureEventHandler) OnReceiveResponse(resp legacyproto.Message) {
	if h.err != nil || h.formatter == nil {
		return
	}

	formatted, err := h.formatter(resp)
	if err != nil {
		h.err = err
		return
	}

	h.responses = append(h.responses, []byte(strings.TrimSpace(formatted)))
}

func (h *grpcCaptureEventHandler) OnReceiveTrailers(stat *status.Status, md metadata.MD) {
	h.status = stat
}

func (h *grpcCaptureEventHandler) Error() error {
	return h.err
}

func (h *grpcCaptureEventHandler) Status() *status.Status {
	return h.status
}

func (h *grpcCaptureEventHandler) ResponsePayload() []byte {
	switch len(h.responses) {
	case 0:
		return nil
	case 1:
		return h.responses[0]
	default:
		return joinResponses(h.responses)
	}
}

func joinResponses(responses [][]byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for idx, resp := range responses {
		if idx > 0 {
			buf.WriteByte(',')
		}
		buf.Write(bytes.TrimSpace(resp))
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

type compositeDescriptorSource struct {
	reflection grpcurl.DescriptorSource
	file       grpcurl.DescriptorSource
}

func (cs compositeDescriptorSource) ListServices() ([]string, error) {
	return cs.reflection.ListServices()
}

func (cs compositeDescriptorSource) FindSymbol(name string) (desc.Descriptor, error) {
	if d, err := cs.reflection.FindSymbol(name); err == nil {
		return d, nil
	}
	return cs.file.FindSymbol(name)
}

func (cs compositeDescriptorSource) AllExtensionsForType(typeName string) ([]*desc.FieldDescriptor, error) {
	exts, err := cs.reflection.AllExtensionsForType(typeName)
	if err != nil {
		return cs.file.AllExtensionsForType(typeName)
	}

	tags := make(map[int32]struct{}, len(exts))
	for _, ext := range exts {
		tags[ext.GetNumber()] = struct{}{}
	}

	fileExts, err := cs.file.AllExtensionsForType(typeName)
	if err != nil {
		return exts, nil
	}

	for _, ext := range fileExts {
		if _, ok := tags[ext.GetNumber()]; ok {
			continue
		}
		exts = append(exts, ext)
	}

	return exts, nil
}

func executeSQLAndMaybeSave(ctx context.Context, db *sql.DB, step Step, sqlStmt string, vars map[string]string) (int, error) {
	if len(step.Save) == 0 {
		return runSQLWithoutSave(ctx, db, step, sqlStmt)
	}

	return runSQLAndSave(ctx, db, step, sqlStmt, vars)
}

func runSQLWithoutSave(ctx context.Context, db *sql.DB, step Step, sqlStmt string) (int, error) {
	results, err := db.ExecContext(ctx, sqlStmt)
	if err != nil {
		return 0, fmt.Errorf("execute sql for step %q: %w", step.Name, err)
	}

	rowsAffected, err := results.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("get affected rows for step %q: %w", step.Name, err)
	}

	return int(rowsAffected), nil
}

func runSQLAndSave(ctx context.Context, db *sql.DB, step Step, sqlStmt string, vars map[string]string) (int, error) {
	rows, err := db.QueryContext(ctx, sqlStmt)
	if err != nil {
		return 0, fmt.Errorf("query sql for step %q: %w", step.Name, err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return 0, fmt.Errorf("fetch columns for step %q: %w", step.Name, err)
	}

	columnIndex := make(map[string]int, len(columns))
	for i, col := range columns {
		columnIndex[strings.ToLower(col)] = i
	}

	values := make([]any, len(columns))
	scanTargets := make([]any, len(columns))

	for i := range values {
		scanTargets[i] = &values[i]
	}

	affectedRows := 0
	savedFirstRow := false

	for rows.Next() {
		if err := rows.Scan(scanTargets...); err != nil {
			return 0, fmt.Errorf("scan row for step %q: %w", step.Name, err)
		}

		if !savedFirstRow {
			if err := saveRowValues(step, vars, values, columnIndex); err != nil {
				return 0, err
			}

			savedFirstRow = true
		}

		affectedRows++
	}

	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate rows for step %q: %w", step.Name, err)
	}

	if affectedRows == 0 {
		return 0, fmt.Errorf("execute sql for step %q: no rows returned to save", step.Name)
	}

	return affectedRows, nil
}

func saveRowValues(step Step, vars map[string]string, rowValues []any, columnIndex map[string]int) error {
	for varName, column := range step.Save {
		target := strings.TrimSpace(column)
		if target == "" {
			target = varName
		}

		idx, ok := columnIndex[strings.ToLower(target)]
		if !ok {
			return fmt.Errorf("step %q: column %q not found in result set", step.Name, target)
		}

		val := rowValues[idx]
		if val == nil {
			continue
		}

		text := anyToString(val)
		vars[varName] = text
		fmt.Printf("   %ssaved%s %s = %s\n",
			colorGray,
			colorReset,
			varName,
			trimLongString(text),
		)
	}

	return nil
}

func anyToString(val any) string {
	switch v := val.(type) {
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

func ensureExpectedAffectedRows(step Step, affectedRows int) error {
	if step.ExpectAffectedRows == 0 || affectedRows == step.ExpectAffectedRows {
		return nil
	}

	fmt.Printf("%s✖ %s: expected %d affected rows, got %d%s\n",
		colorRed,
		step.Name,
		step.ExpectAffectedRows,
		affectedRows,
		colorReset,
	)

	return fmt.Errorf("step %q failed: unexpected affected rows %d", step.Name, affectedRows)
}

func executeSQLStep(ctx context.Context, step Step, sqlStmt string, vars map[string]string) error {
	dbURL := strings.TrimSpace(render(step.DatabaseURL, vars))
	if dbURL == "" {
		dbURL = strings.TrimSpace(vars["database_url"])
	}

	if dbURL == "" {
		dbURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}

	if dbURL == "" {
		return fmt.Errorf("step %q requires database_url (var, step override, or DATABASE_URL env)", step.Name)
	}

	stepCtx, cancel := context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
	defer cancel()

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return fmt.Errorf("open database for step %q: %w", step.Name, err)
	}
	defer db.Close()

	if err := db.PingContext(stepCtx); err != nil {
		return fmt.Errorf("ping database for step %q: %w", step.Name, err)
	}

	fmt.Printf("%s⇒ %s%s SQL %s%s\n",
		colorBlue,
		step.Name,
		colorReset,
		trimLongString(sqlStmt),
		colorReset,
	)

	affectedRows, err := executeSQLAndMaybeSave(stepCtx, db, step, sqlStmt, vars)
	if err != nil {
		return err
	}

	if err := ensureExpectedAffectedRows(step, affectedRows); err != nil {
		return err
	}

	fmt.Printf("%s✓ %s%s\n", colorGreen, step.Name, colorReset)

	return nil
}

func saveValues(respBytes []byte, save map[string]string, vars map[string]string) {
	saveCount := 0
	for varName, jsonPath := range save {
		val := gjson.GetBytes(respBytes, jsonPath).String()
		if val == "" {
			continue
		}

		vars[varName] = val
		saveCount++
		fmt.Printf("   %ssaved%s %s = %s\n",
			colorGray,
			colorReset,
			varName,
			trimLongString(val),
		)
	}

	if saveCount == 0 && len(save) > 0 {
		fmt.Printf("   %sno values saved from response%s\n", colorGray, colorReset)

		// actual response for debugging
		fmt.Printf("   %sresponse: %s%s\n", colorGray, string(respBytes), colorReset)
	}
}

func render(tmpl string, vars map[string]string) string {
	if tmpl == "" {
		return ""
	}

	t, err := template.New("flow").Funcs(templateFuncs).Option("missingkey=zero").Parse(tmpl)
	if err != nil {
		return tmpl
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return tmpl
	}

	return strings.TrimSpace(buf.String())
}

func trimLongString(s string) string {
	if len(s) <= maxDisplayedStringLen {
		return s
	}

	return s[:maxDisplayedStringLen] + "..."
}

var templateFuncs = template.FuncMap{
	"toLower":               strings.ToLower,
	"toUpper":               strings.ToUpper,
	"randString":            randomString,
	"randomAddress":         randomAddress,
	"randomCity":            randomCity,
	"randomColor":           randomColor,
	"randomCompany":         randomCompany,
	"randomCompanyIndustry": randomCompanyIndustry,
	"randomCountry":         randomCountry,
	"randomEmail":           randomEmail,
	"randomInt":             randomInt,
	"randomJobTitle":        randomJobTitle,
	"randomName":            randomName,
	"randomParagraph":       randomParagraph,
	"randomPhone":           randomPhone,
	"randomSentence":        randomSentence,
	"randomUUID":            uuid.NewString,
	"randomWebsite":         randomWebsite,
	"randomZipCode":         randomZipCode,
}
