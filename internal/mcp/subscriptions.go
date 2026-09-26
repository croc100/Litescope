package mcp

import (
	"encoding/json"
	"os"
	"time"
)

// subscriptions/listen (MCP 2026-07-28) replaces resources/subscribe and the
// standalone HTTP GET stream. One listen request opens one long-lived stream:
// the server acknowledges it, then pushes only the notification types the
// client opted into, each tagged with the subscription's id so a client can
// demultiplex several streams sharing one stdio channel.
//
// The handshake-era resources/subscribe path is kept alongside it for legacy
// clients; both feed the same watcher goroutine.

// subFilter is the client's notifications filter on subscriptions/listen.
type subFilter struct {
	ToolsListChanged      bool     `json:"toolsListChanged"`
	PromptsListChanged    bool     `json:"promptsListChanged"`
	ResourcesListChanged  bool     `json:"resourcesListChanged"`
	ResourceSubscriptions []string `json:"resourceSubscriptions"`
}

// subscription is one open subscriptions/listen stream.
type subscription struct {
	id   json.RawMessage // JSON-RPC id of the listen request; also the subscription id
	uris map[string]bool // resource URIs this stream watches
	sink func([]byte)    // where this stream's messages go
	done chan struct{}   // closed when the stream ends
}

// handleListen registers a subscriptions/listen stream, acknowledges it and
// starts the resource watcher. It deliberately sends no JSON-RPC response: the
// request stays open, and a response is only sent on graceful closure.
//
// sink is where the stream's messages are written. Over stdio that is the one
// shared output stream; over Streamable HTTP it is this request's own SSE
// response body.
func (s *server) handleListen(req rpcRequest, rc *reqCtx, sink func([]byte)) *subscription {
	var p struct {
		Notifications subFilter `json:"notifications"`
	}
	_ = json.Unmarshal(req.Params, &p)

	sub := &subscription{
		id:   append(json.RawMessage(nil), req.ID...),
		uris: map[string]bool{},
		sink: sink,
		done: make(chan struct{}),
	}
	for _, u := range p.Notifications.ResourceSubscriptions {
		if u != "" {
			sub.uris[u] = true
		}
	}

	s.mu.Lock()
	s.listens[string(sub.id)] = sub
	start := !s.watching
	if start {
		s.watching = true
	}
	s.mu.Unlock()
	if start {
		go s.watchResources()
	}

	// The acknowledgement must be the first message on the subscription, and
	// reflects only the notification types we actually honour. Litescope's
	// tool/prompt/resource lists are fixed for the life of the process, so the
	// three list-changed types are never emitted and are dropped from the ack.
	ack := map[string]interface{}{}
	if len(p.Notifications.ResourceSubscriptions) > 0 {
		uris := make([]string, 0, len(sub.uris))
		for _, u := range p.Notifications.ResourceSubscriptions {
			if u != "" {
				uris = append(uris, u)
			}
		}
		ack["resourceSubscriptions"] = uris
	}
	s.notifySub(sub, "notifications/subscriptions/acknowledged", map[string]interface{}{
		"notifications": ack,
	})
	return sub
}

// closeListen removes a subscription. When graceful is true the server is
// ending the stream on its own initiative, so it answers the original request
// to distinguish a clean close from a dropped transport.
func (s *server) closeListen(sub *subscription, graceful bool) {
	s.mu.Lock()
	if _, ok := s.listens[string(sub.id)]; !ok {
		s.mu.Unlock()
		return
	}
	delete(s.listens, string(sub.id))
	s.mu.Unlock()

	if graceful {
		s.write(sub.sink, rpcResponse{JSONRPC: "2.0", ID: sub.id, Result: map[string]interface{}{
			"resultType": "complete",
			"_meta":      map[string]interface{}{metaSubscriptionID: rawOrString(sub.id)},
		}})
	}
	close(sub.done)
}

// cancelListen handles notifications/cancelled for a listen request (the stdio
// cancellation path; on HTTP the client just closes the stream).
func (s *server) cancelListen(params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(params, &p) != nil || len(p.RequestID) == 0 {
		return
	}
	s.mu.Lock()
	sub := s.listens[string(p.RequestID)]
	s.mu.Unlock()
	if sub != nil {
		s.closeListen(sub, false)
	}
}

// notifySub writes a notification on one subscription's stream, tagged with the
// subscription id as the spec requires.
func (s *server) notifySub(sub *subscription, method string, params map[string]interface{}) {
	meta, _ := params["_meta"].(map[string]interface{})
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta[metaSubscriptionID] = rawOrString(sub.id)
	params["_meta"] = meta
	s.write(sub.sink, map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params})
}

// rawOrString decodes a JSON-RPC id for embedding in a params object, keeping
// its original JSON type (number or string).
func rawOrString(id json.RawMessage) interface{} {
	var v interface{}
	if json.Unmarshal(id, &v) == nil {
		return v
	}
	return string(id)
}

// watchedURIs returns every resource URI any subscriber is interested in, with
// the legacy (resources/subscribe) set flagged separately.
func (s *server) watchedURIs() (all []string, legacy map[string]bool, modern map[string][]*subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	legacy = map[string]bool{}
	modern = map[string][]*subscription{}
	seen := map[string]bool{}
	for u := range s.subs {
		legacy[u] = true
		if !seen[u] {
			seen[u] = true
			all = append(all, u)
		}
	}
	for _, sub := range s.listens {
		for u := range sub.uris {
			modern[u] = append(modern[u], sub)
			if !seen[u] {
				seen[u] = true
				all = append(all, u)
			}
		}
	}
	return all, legacy, modern
}

// resourceUpdated fans one resource change out to every subscriber: untagged to
// legacy resources/subscribe clients, tagged per stream to modern ones.
func (s *server) resourceUpdated(uri string, legacy map[string]bool, modern map[string][]*subscription) {
	if legacy[uri] {
		s.notify("notifications/resources/updated", map[string]interface{}{"uri": uri})
	}
	for _, sub := range modern[uri] {
		s.notifySub(sub, "notifications/resources/updated", map[string]interface{}{"uri": uri})
	}
}

// watchResources polls every subscribed local-file resource and emits
// notifications/resources/updated when it's actually worth telling the agent.
// Remote (d1/turso) resources cannot be watched and are skipped. It exits when
// the connection ends.
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
			uris, legacy, modern := s.watchedURIs()
			for _, u := range uris {
				if sig, ok := liveSignature(u); ok {
					prev, seen := states[u]
					states[u] = sig
					if seen && sig != prev {
						s.resourceUpdated(u, legacy, modern)
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
					s.resourceUpdated(u, legacy, modern)
				}
			}
		}
	}
}
