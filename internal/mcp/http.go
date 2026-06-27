package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// ServeHTTP runs the MCP server over the Streamable HTTP transport (MCP
// 2025-06-18) on addr. A single endpoint (default "/mcp") accepts:
//
//   - POST — one JSON-RPC message. A request returns its response as the body
//     (application/json); a notification/response returns 202 with no body.
//   - GET  — opens a Server-Sent Events stream that carries server-initiated
//     notifications (logging, resource updates) for the session.
//   - DELETE — terminates the session.
//
// initialize creates a session and returns its id in the Mcp-Session-Id
// response header; every later request must echo that header. allowWrites and
// defaultSource apply to every session, exactly as for the stdio transport.
func ServeHTTP(addr, path, version string, allowWrites bool, defaultSource string) error {
	if path == "" {
		path = "/mcp"
	}
	h := &httpTransport{
		version: version, allowWrites: allowWrites, defaultSource: defaultSource,
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

func (h *httpTransport) handlePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	var probe struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &probe)

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
