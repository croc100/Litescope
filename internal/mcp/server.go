package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/croc100/litescope/internal/connector"
)

// protocolVersion is the MCP revision this server implements.
const protocolVersion = "2025-06-18"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// server holds the per-connection state shared across requests.
type server struct {
	tools         []Tool
	byName        map[string]Tool
	prompts       []Prompt
	promptByName  map[string]Prompt
	version       string
	defaultSource string // optional database bound at startup for concrete resources

	// mu guards the sinks and the mutable fields below, so the resource watcher
	// goroutine and the request loop can both emit messages safely.
	mu sync.Mutex
	// respondSink receives JSON-RPC responses (replies to a request); notifySink
	// receives server-initiated notifications (logging, resource updates). Over
	// stdio both write to the same stream; over Streamable HTTP responses go back
	// on the POST body while notifications go to the session's SSE stream.
	respondSink func([]byte)
	notifySink  func([]byte)
	logLevel    string          // current minimum log level (RFC 5424 names)
	subs        map[string]bool // subscribed resource URIs
	watching    bool            // true once the resource watcher goroutine is running
	stop        chan struct{}   // closed when the connection ends, stopping the watcher
}

// newServer builds a server with its tool/prompt registries populated. The
// caller wires respondSink/notifySink for its transport (stdio or HTTP).
func newServer(version string, allowWrites bool, defaultSource string) *server {
	tools := Registry(allowWrites)
	byName := make(map[string]Tool, len(tools))
	for _, t := range tools {
		byName[t.Name] = t
	}
	prompts := Prompts()
	promptByName := make(map[string]Prompt, len(prompts))
	for _, p := range prompts {
		promptByName[p.Name] = p
	}
	return &server{
		tools: tools, byName: byName,
		prompts: prompts, promptByName: promptByName,
		version: version, defaultSource: defaultSource,
		logLevel: "info",
		subs:     map[string]bool{},
		stop:     make(chan struct{}),
	}
}

// Serve runs the MCP server over newline-delimited JSON-RPC on the given
// streams until in reaches EOF. stdout carries only protocol messages.
// When allowWrites is true, write-capable tools (litescope_query_write,
// litescope_migrate_apply, litescope_d1_create, litescope_d1_delete) are
// included; otherwise only read-only tools are exposed. defaultSource, when
// non-empty, is exposed as concrete schema/dictionary resources.
func Serve(in io.Reader, out io.Writer, version string, allowWrites bool, defaultSource string) error {
	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)

	s := newServer(version, allowWrites, defaultSource)
	// Over stdio, responses and notifications share one newline-delimited stream.
	line := func(b []byte) {
		writer.Write(b)
		writer.WriteByte('\n')
		writer.Flush()
	}
	s.respondSink = line
	s.notifySink = line
	defer close(s.stop)

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			s.handleLine(line)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *server) handleLine(line []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return // ignore malformed input
	}
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		// Agree to the client's requested protocol version when it sends one;
		// otherwise fall back to ours.
		ver := protocolVersion
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(req.Params, &p) == nil && p.ProtocolVersion != "" {
			ver = p.ProtocolVersion
		}
		s.respond(req.ID, map[string]interface{}{
			"protocolVersion": ver,
			"capabilities": map[string]interface{}{
				"tools":       map[string]interface{}{},
				"prompts":     map[string]interface{}{},
				"resources":   map[string]interface{}{"subscribe": true},
				"logging":     map[string]interface{}{},
				"completions": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{"name": "litescope", "version": s.version},
		})
	case "notifications/initialized", "notifications/cancelled":
		// notifications: no response
	case "ping":
		s.respond(req.ID, map[string]interface{}{})
	case "logging/setLevel":
		var p struct {
			Level string `json:"level"`
		}
		if json.Unmarshal(req.Params, &p) == nil && logSeverity(p.Level) >= 0 {
			s.mu.Lock()
			s.logLevel = p.Level
			s.mu.Unlock()
		}
		s.respond(req.ID, map[string]interface{}{})
	case "tools/list":
		// cursor is accepted for spec compliance; the full set fits in one page,
		// so no nextCursor is returned.
		s.respond(req.ID, map[string]interface{}{"tools": toolDescriptors(s.tools)})
	case "tools/call":
		s.handleToolCall(req)
	case "prompts/list":
		s.respond(req.ID, map[string]interface{}{"prompts": promptDescriptors(s.prompts)})
	case "prompts/get":
		s.handlePromptGet(req)
	case "resources/list":
		s.respond(req.ID, map[string]interface{}{"resources": concreteResources(s.defaultSource)})
	case "resources/templates/list":
		s.respond(req.ID, map[string]interface{}{"resourceTemplates": resourceTemplates()})
	case "resources/read":
		s.handleResourceRead(req)
	case "resources/subscribe":
		s.handleSubscribe(req, true)
	case "resources/unsubscribe":
		s.handleSubscribe(req, false)
	case "completion/complete":
		s.handleComplete(req)
	default:
		if !isNotification {
			s.respondError(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func (s *server) handleToolCall(req rpcRequest) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.respondError(req.ID, -32602, "invalid params")
		return
	}
	tool, ok := s.byName[params.Name]
	if !ok {
		s.respondError(req.ID, -32602, "unknown tool: "+params.Name)
		return
	}
	s.log("debug", map[string]interface{}{"event": "tool_call", "tool": params.Name})
	text, err := tool.Handler(params.Arguments)
	if err != nil {
		// Tool-level errors are returned in the result with isError, not as a
		// protocol error, so the model can read and react to them.
		s.log("error", map[string]interface{}{"event": "tool_error", "tool": params.Name, "error": err.Error()})
		s.respond(req.ID, toolResult(fmt.Sprintf("Error: %v", err), true))
		return
	}
	s.respond(req.ID, toolResult(text, false))
}

// structuredOf parses a tool's JSON text output into an object for the
// structuredContent field (MCP 2025-06-18). Every litescope tool emits a JSON
// object via toJSON, so this lets clients consume results without re-parsing the
// text block themselves. Returns nil (omitting structuredContent) when the text
// is not a JSON object — e.g. an error string.
func structuredOf(text string) map[string]interface{} {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return nil
	}
	return obj
}

func (s *server) handlePromptGet(req rpcRequest) {
	var params struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.respondError(req.ID, -32602, "invalid params")
		return
	}
	p, ok := s.promptByName[params.Name]
	if !ok {
		s.respondError(req.ID, -32602, "unknown prompt: "+params.Name)
		return
	}
	for _, a := range p.Arguments {
		if a.Required && params.Arguments[a.Name] == "" {
			s.respondError(req.ID, -32602, "missing required argument: "+a.Name)
			return
		}
	}
	s.respond(req.ID, map[string]interface{}{
		"description": p.Description,
		"messages": []map[string]interface{}{{
			"role":    "user",
			"content": map[string]interface{}{"type": "text", "text": p.Render(params.Arguments)},
		}},
	})
}

func (s *server) handleResourceRead(req rpcRequest) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.respondError(req.ID, -32602, "invalid params")
		return
	}
	text, mime, err := readResource(params.URI)
	if err != nil {
		s.respondError(req.ID, -32602, err.Error())
		return
	}
	s.respond(req.ID, map[string]interface{}{
		"contents": []map[string]interface{}{{
			"uri":      params.URI,
			"mimeType": mime,
			"text":     text,
		}},
	})
}

// handleSubscribe records (or removes) a resource subscription and starts the
// watcher goroutine on the first subscribe. The watcher emits
// notifications/resources/updated when a local-file-backed resource changes.
func (s *server) handleSubscribe(req rpcRequest, subscribe bool) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || p.URI == "" {
		s.respondError(req.ID, -32602, "invalid params")
		return
	}
	s.mu.Lock()
	if subscribe {
		s.subs[p.URI] = true
	} else {
		delete(s.subs, p.URI)
	}
	start := subscribe && !s.watching
	if start {
		s.watching = true
	}
	s.mu.Unlock()
	if start {
		go s.watchResources()
	}
	s.respond(req.ID, map[string]interface{}{})
}

// watchResources polls every subscribed local-file resource and emits
// notifications/resources/updated when it's actually worth telling the agent.
// Remote (d1/turso) resources cannot be watched and are skipped. It exits when
// Serve returns.
//
// Two different triggers are used depending on the resource:
//   - schema/dictionary rarely change, so any file-mtime bump is notification-
//     worthy.
//   - health/locks are live diagnoses of a file that may be written constantly;
//     using mtime here would fire on every single write even when nothing about
//     severity changed, and — worse — would never fire when writes *stop*
//     (a stale heartbeat, the exact case the check exists to catch). Instead
//     these recompute the diagnosis each tick and notify only when the
//     severity/verdict signature changes (see liveSignature).
func (s *server) watchResources() {
	mtimes := map[string]time.Time{}
	states := map[string]string{}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			uris := make([]string, 0, len(s.subs))
			for u := range s.subs {
				uris = append(uris, u)
			}
			s.mu.Unlock()
			for _, u := range uris {
				if sig, ok := liveSignature(u); ok {
					prev, seen := states[u]
					states[u] = sig
					if seen && sig != prev {
						s.notify("notifications/resources/updated", map[string]interface{}{"uri": u})
					}
					continue
				}
				path := resourceFilePath(u)
				if path == "" {
					continue // remote or unknown URI: not watchable
				}
				fi, err := os.Stat(path)
				if err != nil {
					continue
				}
				mt := fi.ModTime()
				prev, seen := mtimes[u]
				mtimes[u] = mt
				if seen && mt.After(prev) {
					s.notify("notifications/resources/updated", map[string]interface{}{"uri": u})
				}
			}
		}
	}
}

// handleComplete answers completion/complete for argument autocompletion. The
// only argument we can meaningfully complete is a database "source" (also
// exposed as old/new on diff/migrate tools): we suggest the bound default
// source and, when Cloudflare credentials are present, the account's D1 DSNs.
func (s *server) handleComplete(req rpcRequest) {
	var p struct {
		Argument struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"argument"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.respondError(req.ID, -32602, "invalid params")
		return
	}
	var values []string
	switch p.Argument.Name {
	case "source", "old", "new":
		values = s.completeSource(p.Argument.Value)
	}
	s.respond(req.ID, map[string]interface{}{
		"completion": map[string]interface{}{
			"values":  values,
			"total":   len(values),
			"hasMore": false,
		},
	})
}

func (s *server) completeSource(prefix string) []string {
	out := []string{}
	add := func(v string) {
		if v != "" && strings.HasPrefix(v, prefix) {
			out = append(out, v)
		}
	}
	if s.defaultSource != "" {
		add(s.defaultSource)
	}
	// Best-effort: list D1 databases when credentials are configured. Errors are
	// swallowed so completion never fails the request.
	if os.Getenv("CLOUDFLARE_API_TOKEN") != "" && os.Getenv("CLOUDFLARE_ACCOUNT_ID") != "" {
		if dbs, err := connector.ListD1Databases(); err == nil {
			for _, db := range dbs {
				add(db.DSN)
			}
		}
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func toolDescriptors(tools []Tool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		d := map[string]interface{}{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
			"annotations": annotationsFor(t.Name),
		}
		if t.OutputSchema != nil {
			d["outputSchema"] = t.OutputSchema
		}
		if title := annotationsFor(t.Name).Title; title != "" {
			d["title"] = title
		}
		out = append(out, d)
	}
	return out
}

func toolResult(text string, isErr bool) map[string]interface{} {
	res := map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": isErr,
	}
	// Mirror successful JSON output into structuredContent so agents can consume
	// it directly instead of parsing the text block (MCP 2025-06-18).
	if !isErr {
		if obj := structuredOf(text); obj != nil {
			res["structuredContent"] = obj
		}
	}
	return res
}

// ── message writing (thread-safe) ───────────────────────────────────────────

func (s *server) respond(id json.RawMessage, result interface{}) {
	s.write(s.respondSink, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *server) respondError(id json.RawMessage, code int, msg string) {
	s.write(s.respondSink, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

// notify writes a JSON-RPC notification (no id) to the client.
func (s *server) notify(method string, params interface{}) {
	s.write(s.notifySink, map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params})
}

// log emits a notifications/message log record when level passes the client's
// configured minimum (set via logging/setLevel; default "info").
func (s *server) log(level string, data interface{}) {
	s.mu.Lock()
	min := s.logLevel
	s.mu.Unlock()
	if logSeverity(level) < logSeverity(min) {
		return
	}
	s.notify("notifications/message", map[string]interface{}{
		"level": level, "logger": "litescope", "data": data,
	})
}

// write marshals v and hands it to the given sink under the server lock so the
// watcher goroutine and the request loop never interleave on the same stream.
func (s *server) write(sink func([]byte), v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sink != nil {
		sink(b)
	}
}

// logSeverity maps RFC 5424 syslog level names to their numeric severity, used
// to compare a message's level against the configured minimum. Unknown names
// return -1.
func logSeverity(level string) int {
	switch level {
	case "debug":
		return 0
	case "info":
		return 1
	case "notice":
		return 2
	case "warning":
		return 3
	case "error":
		return 4
	case "critical":
		return 5
	case "alert":
		return 6
	case "emergency":
		return 7
	default:
		return -1
	}
}
