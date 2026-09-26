package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/croc100/litescope/internal/connector"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// server holds the state shared across requests on one connection. Under the
// modern (stateless) revision nothing about a request is inferred from an
// earlier one; the mutable state below belongs either to the handshake-era
// session or to an explicitly opened subscription stream.
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
	// receives server-initiated notifications for the handshake-era session.
	// Over stdio both write to the same stream; over Streamable HTTP responses
	// go back on the POST body. Modern subscription streams carry their own
	// sink (see subscription.sink).
	respondSink func([]byte)
	notifySink  func([]byte)
	logLevel    string                   // handshake-era minimum log level (RFC 5424 names)
	subs        map[string]bool          // legacy resources/subscribe URIs
	listens     map[string]*subscription // open subscriptions/listen streams, by request id
	watching    bool                     // true once the resource watcher goroutine is running
	stop        chan struct{}            // closed when the connection ends, stopping the watcher
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
		listens:  map[string]*subscription{},
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

// handleLine dispatches one message on a stdio-style shared stream, where a
// subscription's notifications go to the same sink as everything else.
func (s *server) handleLine(line []byte) {
	s.handleMessage(line, s.notifySinkFn())
}

func (s *server) notifySinkFn() func([]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notifySink
}

// handleMessage dispatches one JSON-RPC message. listenSink is the stream a
// subscriptions/listen request writes to; it returns the subscription it opened
// so a transport that gives each request its own stream (Streamable HTTP) can
// hold that stream open and tear it down when the client goes away.
func (s *server) handleMessage(line []byte, listenSink func([]byte)) *subscription {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return nil // ignore malformed input
	}
	isNotification := len(req.ID) == 0

	// Decide the era of this single request. A request carrying
	// io.modelcontextprotocol/protocolVersion is served statelessly under that
	// revision; anything else is handshake-era.
	meta := parseRequestMeta(req.Params)
	rc := legacyCtx()
	switch {
	case meta.hasVersion:
		if !supportsVersion(meta.version) {
			if !isNotification {
				s.respondErrorData(req.ID, errUnsupportedVersion, "unsupported protocol version",
					map[string]interface{}{"supported": supportedVersions(), "requested": meta.version})
			}
			return nil
		}
		if meta.version == protocolVersion {
			rc = &reqCtx{modern: true, version: meta.version, logLevel: meta.logLevel}
			// protocolVersion and clientCapabilities are both required on every
			// modern request; a request missing one is malformed.
			if !meta.hasClientCaps {
				if !isNotification {
					s.respondError(req.ID, -32602, "missing required _meta field: "+metaClientCaps)
				}
				return nil
			}
		}
	case isModernOnlyMethod(req.Method):
		// server/discover doubles as the client's backward-compatibility probe,
		// so answer it even when the probe carries no metadata at all.
		rc = &reqCtx{modern: true, version: protocolVersion}
	}

	if rc.modern && isRemovedInModern(req.Method) {
		if !isNotification {
			s.respondError(req.ID, -32601, "method not found in "+protocolVersion+": "+req.Method)
		}
		return nil
	}

	switch req.Method {
	case "server/discover":
		s.reply(rc, req.ID, req.Method, "", map[string]interface{}{
			"supportedVersions": supportedVersions(),
			"capabilities":      s.capabilities(rc),
			"instructions": "Operations layer for SQLite, Cloudflare D1 and Turso. Diagnose before a write " +
				"(health, locks, advise, lint), and rewind after one (every write tool captures an undo point).",
		})
	case "subscriptions/listen":
		if isNotification {
			return nil
		}
		return s.handleListen(req, rc, listenSink)
	case "initialize":
		// Agree to the client's requested protocol version when it sends one;
		// otherwise fall back to the handshake-era default.
		ver := legacyVersion
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(req.Params, &p) == nil && p.ProtocolVersion != "" {
			ver = p.ProtocolVersion
		}
		s.reply(rc, req.ID, req.Method, "", map[string]interface{}{
			"protocolVersion": ver,
			"capabilities":    s.capabilities(rc),
			"serverInfo":      map[string]interface{}{"name": "litescope", "version": s.version},
		})
	case "notifications/initialized":
		// notification: no response
	case "notifications/cancelled":
		s.cancelListen(req.Params)
	case "ping":
		s.reply(rc, req.ID, req.Method, "", map[string]interface{}{})
	case "logging/setLevel":
		var p struct {
			Level string `json:"level"`
		}
		if json.Unmarshal(req.Params, &p) == nil && logSeverity(p.Level) >= 0 {
			s.mu.Lock()
			s.logLevel = p.Level
			s.mu.Unlock()
		}
		s.reply(rc, req.ID, req.Method, "", map[string]interface{}{})
	case "tools/list":
		// cursor is accepted for spec compliance; the full set fits in one page,
		// so no nextCursor is returned.
		s.reply(rc, req.ID, req.Method, "", map[string]interface{}{"tools": toolDescriptors(s.tools)})
	case "tools/call":
		s.handleToolCall(req, rc)
	case "prompts/list":
		s.reply(rc, req.ID, req.Method, "", map[string]interface{}{"prompts": promptDescriptors(s.prompts)})
	case "prompts/get":
		s.handlePromptGet(req, rc)
	case "resources/list":
		s.reply(rc, req.ID, req.Method, "", map[string]interface{}{"resources": concreteResources(s.defaultSource)})
	case "resources/templates/list":
		s.reply(rc, req.ID, req.Method, "", map[string]interface{}{"resourceTemplates": resourceTemplates()})
	case "resources/read":
		s.handleResourceRead(req, rc)
	case "resources/subscribe":
		s.handleSubscribe(req, rc, true)
	case "resources/unsubscribe":
		s.handleSubscribe(req, rc, false)
	case "completion/complete":
		s.handleComplete(req, rc)
	default:
		if !isNotification {
			s.respondError(req.ID, -32601, "method not found: "+req.Method)
		}
	}
	return nil
}

// capabilities describes what this server supports, per era. Logging, roots
// and sampling are deprecated in the modern revision and are not advertised
// there; subscriptions are opened with subscriptions/listen instead of
// resources/subscribe, but the resources.subscribe flag still signals that
// resource updates are available.
func (s *server) capabilities(rc *reqCtx) map[string]interface{} {
	caps := map[string]interface{}{
		"tools":       map[string]interface{}{},
		"prompts":     map[string]interface{}{},
		"resources":   map[string]interface{}{"subscribe": true},
		"completions": map[string]interface{}{},
	}
	if !rc.modern {
		caps["logging"] = map[string]interface{}{}
	}
	return caps
}

func (s *server) handleToolCall(req rpcRequest, rc *reqCtx) {
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
	s.log(rc, "debug", map[string]interface{}{"event": "tool_call", "tool": params.Name})
	text, err := tool.Handler(params.Arguments)
	if err != nil {
		// Tool-level errors are returned in the result with isError, not as a
		// protocol error, so the model can read and react to them.
		s.log(rc, "error", map[string]interface{}{"event": "tool_error", "tool": params.Name, "error": err.Error()})
		s.reply(rc, req.ID, req.Method, "", toolResult(fmt.Sprintf("Error: %v", err), true))
		return
	}
	s.reply(rc, req.ID, req.Method, "", toolResult(text, false))
}

// structuredOf parses a tool's JSON text output into an object for the
// structuredContent field. Every litescope tool emits a JSON object via toJSON,
// so this lets clients consume results without re-parsing the text block.
// Returns nil (omitting structuredContent) when the text is not a JSON object —
// e.g. an error string.
func structuredOf(text string) map[string]interface{} {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return nil
	}
	return obj
}

func (s *server) handlePromptGet(req rpcRequest, rc *reqCtx) {
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
	s.reply(rc, req.ID, req.Method, "", map[string]interface{}{
		"description": p.Description,
		"messages": []map[string]interface{}{{
			"role":    "user",
			"content": map[string]interface{}{"type": "text", "text": p.Render(params.Arguments)},
		}},
	})
}

func (s *server) handleResourceRead(req rpcRequest, rc *reqCtx) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.respondError(req.ID, -32602, "invalid params")
		return
	}
	text, mime, err := readResource(params.URI)
	if err != nil {
		// Invalid Params, per the 2026-07-28 alignment with JSON-RPC (the old
		// -32002 "resource not found" code is retired).
		s.respondError(req.ID, -32602, err.Error())
		return
	}
	s.reply(rc, req.ID, req.Method, params.URI, map[string]interface{}{
		"contents": []map[string]interface{}{{
			"uri":      params.URI,
			"mimeType": mime,
			"text":     text,
		}},
	})
}

// handleSubscribe records (or removes) a handshake-era resource subscription
// and starts the watcher goroutine on the first subscribe. Modern clients use
// subscriptions/listen instead (see subscriptions.go).
func (s *server) handleSubscribe(req rpcRequest, rc *reqCtx, subscribe bool) {
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
	s.reply(rc, req.ID, req.Method, "", map[string]interface{}{})
}

// handleComplete answers completion/complete for argument autocompletion. The
// only argument we can meaningfully complete is a database "source" (also
// exposed as old/new on diff/migrate tools): we suggest the bound default
// source and, when Cloudflare credentials are present, the account's D1 DSNs.
func (s *server) handleComplete(req rpcRequest, rc *reqCtx) {
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
	s.reply(rc, req.ID, req.Method, "", map[string]interface{}{
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
	// it directly instead of parsing the text block.
	if !isErr {
		if obj := structuredOf(text); obj != nil {
			res["structuredContent"] = obj
		}
	}
	return res
}

// ── message writing (thread-safe) ───────────────────────────────────────────

// reply answers a request, adding the fields the modern revision requires
// (resultType, serverInfo, caching hints) and leaving handshake-era results
// exactly as they were. uri is the resource URI for resources/read, which
// determines its caching hint; it is empty for every other method.
func (s *server) reply(rc *reqCtx, id json.RawMessage, method, uri string, result interface{}) {
	if len(id) == 0 {
		return // notification: nothing to answer
	}
	s.write(s.respondSink, rpcResponse{JSONRPC: "2.0", ID: id, Result: s.decorate(rc, method, uri, result)})
}

func (s *server) respondError(id json.RawMessage, code int, msg string) {
	s.write(s.respondSink, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *server) respondErrorData(id json.RawMessage, code int, msg string, data interface{}) {
	s.write(s.respondSink, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg, Data: data}})
}

// notify writes a JSON-RPC notification (no id) to the handshake-era client.
func (s *server) notify(method string, params interface{}) {
	s.write(s.notifySink, map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params})
}

// log emits a notifications/message log record when level passes the client's
// configured minimum (set via logging/setLevel; default "info").
//
// Logging is deprecated in 2026-07-28 and is gated on the client asking for it
// per request: a server MUST NOT emit notifications/message for a request that
// did not carry io.modelcontextprotocol/logLevel.
func (s *server) log(rc *reqCtx, level string, data interface{}) {
	min := ""
	if rc != nil && rc.modern {
		if rc.logLevel == "" {
			return
		}
		min = rc.logLevel
	} else {
		s.mu.Lock()
		min = s.logLevel
		s.mu.Unlock()
	}
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
