package mcp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ServeHTTP runs the MCP server over the Streamable HTTP transport on addr,
// serving both eras of the protocol on one endpoint (default "/mcp"):
//
// Modern (2026-07-28) — stateless. Every POST is self-contained: it carries its
// protocol version and client capabilities in the body's _meta and mirrors them
// into the standard request headers. No session is minted, and a
// subscriptions/listen request's own response stream carries its notifications.
//
// Legacy (2025-06-18) — the handshake era, kept for older clients:
//
//   - POST — one JSON-RPC message. A request returns its response as the body
//     (application/json); a notification/response returns 202 with no body.
//   - GET  — opens a Server-Sent Events stream that carries server-initiated
//     notifications (logging, resource updates) for the session.
//   - DELETE — terminates the session.
//
// HTTPOptions configures the Streamable HTTP transport's auth layer.
type HTTPOptions struct {
	// Token, when non-empty, is required as "Authorization: Bearer <token>" on
	// every request. Empty means no token is enforced (open endpoint).
	Token string
	// AllowedOrigins is the set of browser Origins permitted, for DNS-rebinding
	// protection. localhost / 127.0.0.1 origins are always allowed; non-browser
	// clients (no Origin header) are always allowed. Empty list means only those
	// defaults are accepted.
	AllowedOrigins []string
}

// initialize creates a session and returns its id in the Mcp-Session-Id
// response header; every later request must echo that header. allowWrites and
// defaultSource apply to every session, exactly as for the stdio transport.
func ServeHTTP(addr, path, version string, allowWrites bool, defaultSource string, opts HTTPOptions) error {
	if path == "" {
		path = "/mcp"
	}
	origins := map[string]bool{}
	for _, o := range opts.AllowedOrigins {
		if o != "" {
			origins[strings.ToLower(strings.TrimRight(o, "/"))] = true
		}
	}
	h := &httpTransport{
		version: version, allowWrites: allowWrites, defaultSource: defaultSource,
		token: opts.Token, origins: origins,
		sessions: map[string]*httpSession{},
	}
	mux := http.NewServeMux()
	mux.Handle(path, h)
	srv := &http.Server{Addr: addr, Handler: mux}
	return srv.ListenAndServe()
}

type httpTransport struct {
	version       string
	allowWrites   bool
	defaultSource string
	token         string          // required Bearer token; empty = open
	origins       map[string]bool // allowed browser Origins (lowercased, no trailing /)

	mu       sync.Mutex
	sessions map[string]*httpSession
}

// httpSession is one client connection's state: its MCP server plus the SSE
// stream (if any) over which notifications are delivered.
type httpSession struct {
	id  string
	srv *server

	postMu sync.Mutex // serializes POST handling (response capture is per-session)

	sseMu   sync.Mutex
	sseW    io.Writer
	flusher http.Flusher
}

func (h *httpTransport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.handlePost(w, r)
	case http.MethodGet:
		h.handleGet(w, r)
	case http.MethodDelete:
		h.handleDelete(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// authorize enforces the Origin allowlist (DNS-rebinding protection) and the
// Bearer token. It writes the error response and returns false on rejection.
func (h *httpTransport) authorize(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && !h.originAllowed(origin) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return false
	}
	if h.token != "" {
		const prefix = "Bearer "
		got := r.Header.Get("Authorization")
		if !strings.HasPrefix(got, prefix) ||
			subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(got, prefix)), []byte(h.token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
	}
	return true
}

// originAllowed permits localhost origins and any explicitly allowlisted origin.
func (h *httpTransport) originAllowed(origin string) bool {
	norm := strings.ToLower(strings.TrimRight(origin, "/"))
	if h.origins[norm] {
		return true
	}
	if u, err := url.Parse(norm); err == nil {
		switch host := u.Hostname(); host {
		case "localhost", "127.0.0.1", "::1", "[::1]":
			return true
		}
	}
	return false
}

// postProbe is the part of a POSTed message the transport itself needs to see:
// which method it is, whether it is a request, and the params the standard
// headers must mirror.
type postProbe struct {
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id"`
	Params json.RawMessage `json:"params"`
}

func (h *httpTransport) handlePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	var probe postProbe
	_ = json.Unmarshal(body, &probe)

	// Carrying a protocol version in _meta is itself the modern convention, so
	// any request that does is served statelessly — including one naming a
	// version we do not support, which must come back as a recognizable modern
	// error rather than a bare 404. server/discover and subscriptions/listen
	// exist only in the modern revision, so they take that path too even when a
	// bare probe omits the metadata entirely.
	meta := parseRequestMeta(probe.Params)
	switch {
	case meta.hasVersion:
		h.handleModernPost(w, r, body, probe, meta, true)
		return
	case isModernOnlyMethod(probe.Method):
		h.handleModernPost(w, r, body, probe, meta, false)
		return
	}

	sid := r.Header.Get("Mcp-Session-Id")
	var se *httpSession
	newSession := false
	if probe.Method == "initialize" {
		// A fresh handshake mints a new session regardless of any stale header.
		se = h.newSession()
		newSession = true
	} else {
		se = h.session(sid)
		if se == nil {
			http.Error(w, "unknown or missing Mcp-Session-Id", http.StatusNotFound)
			return
		}
	}

	resp, isResponse := se.dispatch(body)
	if newSession {
		w.Header().Set("Mcp-Session-Id", se.id)
	}
	if !isResponse {
		// Notification or client response: nothing to return.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

// handleModernPost serves one stateless request. enforceHeaders is true when
// the body declared the modern protocol version, which is also when the
// transport's standard request headers are required to mirror it.
func (h *httpTransport) handleModernPost(w http.ResponseWriter, r *http.Request, body []byte, probe postProbe, meta requestMeta, enforceHeaders bool) {
	if enforceHeaders {
		if msg, ok := validateRequestHeaders(r, probe.Method, probe.Params, meta.version); !ok {
			writeRPCError(w, http.StatusBadRequest, probe.ID, errHeaderMismatch, msg)
			return
		}
	}

	srv := newServer(h.version, h.allowWrites, h.defaultSource)
	if probe.Method == "subscriptions/listen" && len(probe.ID) > 0 {
		h.serveListenStream(w, r, srv, body)
		return
	}
	defer close(srv.stop)

	var captured []byte
	srv.respondSink = func(b []byte) {
		if captured == nil {
			captured = b
		}
	}
	srv.handleMessage(body, nil)
	if captured == nil {
		w.WriteHeader(http.StatusAccepted) // notification: accepted, no body
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusForResult(captured))
	w.Write(captured)
}

// serveListenStream answers a subscriptions/listen request with its own SSE
// stream, which stays open until the client disconnects. Closing the stream is
// the cancellation signal — there is no session to tear down.
func (h *httpTransport) serveListenStream(w http.ResponseWriter, r *http.Request, srv *server, body []byte) {
	defer close(srv.stop)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Tell reverse proxies not to buffer, or notifications arrive in batches.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var mu sync.Mutex
	sink := func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	srv.respondSink = sink
	srv.notifySink = sink

	sub := srv.handleMessage(body, sink)
	if sub == nil {
		return
	}
	// Keep the connection alive through quiet periods: an SSE comment line is
	// ignored by clients but stops idle intermediaries from dropping the stream.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			srv.closeListen(sub, false)
			return
		case <-sub.done:
			return
		case <-ticker.C:
			mu.Lock()
			fmt.Fprint(w, ":\n\n")
			flusher.Flush()
			mu.Unlock()
		}
	}
}

// validateRequestHeaders enforces the standard request headers the modern
// transport requires. They mirror body fields so intermediaries can route
// without parsing the body, so a mismatch between the two is rejected rather
// than silently resolved in favour of either side.
func validateRequestHeaders(r *http.Request, method string, params json.RawMessage, version string) (string, bool) {
	if got := r.Header.Get("MCP-Protocol-Version"); got != version {
		if got == "" {
			return "missing required header: MCP-Protocol-Version", false
		}
		return fmt.Sprintf("MCP-Protocol-Version header %q does not match body value %q", got, version), false
	}
	if got := r.Header.Get("Mcp-Method"); got != method {
		if got == "" {
			return "missing required header: Mcp-Method", false
		}
		return fmt.Sprintf("Mcp-Method header %q does not match body value %q", got, method), false
	}
	want, needsName := headerName(method, params)
	if !needsName {
		return "", true
	}
	got, err := decodeHeaderValue(r.Header.Get("Mcp-Name"))
	if err != nil {
		return "Mcp-Name header is not valid base64", false
	}
	if got == "" {
		return "missing required header: Mcp-Name", false
	}
	if got != want {
		return fmt.Sprintf("Mcp-Name header %q does not match body value %q", got, want), false
	}
	return "", true
}

// headerName returns the body value the Mcp-Name header must mirror: the tool
// or prompt name, or the resource URI. needsName is false for methods that do
// not carry one.
func headerName(method string, params json.RawMessage) (value string, needsName bool) {
	var p struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	_ = json.Unmarshal(params, &p)
	switch method {
	case "tools/call", "prompts/get":
		return p.Name, true
	case "resources/read":
		return p.URI, true
	}
	return "", false
}

// decodeHeaderValue unwraps the base64 sentinel form clients must use for
// header values that cannot be carried as plain ASCII.
func decodeHeaderValue(v string) (string, error) {
	const pre, suf = "=?base64?", "?="
	if !strings.HasPrefix(v, pre) || !strings.HasSuffix(v, suf) {
		return v, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(v, pre), suf))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// statusForResult maps a JSON-RPC error in a modern response to its HTTP
// status. Only the errors the spec defines for the transport change the status:
// a client uses them to tell a modern server from a legacy one, so ordinary
// application errors must keep returning 200 lest a client fall back.
func statusForResult(resp []byte) int {
	var m struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(resp, &m) != nil || m.Error == nil {
		return http.StatusOK
	}
	switch m.Error.Code {
	case errHeaderMismatch, errMissingClientCap, errUnsupportedVersion:
		return http.StatusBadRequest
	case -32601:
		return http.StatusNotFound
	}
	return http.StatusOK
}

// writeRPCError sends a JSON-RPC error response with an explicit HTTP status.
func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (h *httpTransport) handleGet(w http.ResponseWriter, r *http.Request) {
	se := h.session(r.Header.Get("Mcp-Session-Id"))
	if se == nil {
		http.Error(w, "unknown or missing Mcp-Session-Id", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	se.attachSSE(w, flusher)
	defer se.detachSSE()

	// Hold the stream open until the client disconnects.
	<-r.Context().Done()
}

func (h *httpTransport) handleDelete(w http.ResponseWriter, r *http.Request) {
	sid := r.Header.Get("Mcp-Session-Id")
	h.mu.Lock()
	se, ok := h.sessions[sid]
	if ok {
		delete(h.sessions, sid)
	}
	h.mu.Unlock()
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	close(se.srv.stop) // stop the resource watcher
	w.WriteHeader(http.StatusNoContent)
}

func (h *httpTransport) newSession() *httpSession {
	se := &httpSession{
		id:  newSessionID(),
		srv: newServer(h.version, h.allowWrites, h.defaultSource),
	}
	// Notifications go to the SSE stream when one is attached; dropped otherwise.
	se.srv.notifySink = se.pushSSE
	h.mu.Lock()
	h.sessions[se.id] = se
	h.mu.Unlock()
	return se
}

func (h *httpTransport) session(id string) *httpSession {
	if id == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[id]
}

// dispatch handles one POSTed message and returns the captured JSON-RPC
// response. isResponse is false for client notifications/responses (which
// produce no reply). POSTs are serialized per session so the per-call response
// capture cannot interleave.
func (se *httpSession) dispatch(body []byte) (resp []byte, isResponse bool) {
	se.postMu.Lock()
	defer se.postMu.Unlock()
	var captured []byte
	se.srv.respondSink = func(b []byte) {
		// Only the first (and only) response for the request is expected.
		if captured == nil {
			captured = b
		}
	}
	se.srv.handleLine(body)
	se.srv.respondSink = nil
	return captured, captured != nil
}

func (se *httpSession) attachSSE(w io.Writer, f http.Flusher) {
	se.sseMu.Lock()
	se.sseW, se.flusher = w, f
	se.sseMu.Unlock()
}

func (se *httpSession) detachSSE() {
	se.sseMu.Lock()
	se.sseW, se.flusher = nil, nil
	se.sseMu.Unlock()
}

func (se *httpSession) pushSSE(b []byte) {
	se.sseMu.Lock()
	defer se.sseMu.Unlock()
	if se.flusher == nil {
		return // no stream attached: drop the notification
	}
	fmt.Fprintf(se.sseW, "data: %s\n\n", b)
	se.flusher.Flush()
}

func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
