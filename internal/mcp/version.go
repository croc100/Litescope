package mcp

import "encoding/json"

// Litescope speaks two eras of MCP at once (a "dual-era" server, in the words
// of the 2026-07-28 spec):
//
//   - modern (2026-07-28) — stateless. There is no initialize handshake: every
//     request carries its protocol version and the client's capabilities in
//     params._meta, and the server answers each request independently.
//   - legacy (2025-06-18) — the handshake era. A client opens with initialize,
//     and the negotiated version holds for the stdio process / HTTP session.
//
// Which era a request belongs to is decided per request, by the presence of
// io.modelcontextprotocol/protocolVersion in its _meta. Legacy clients keep
// working unchanged; modern clients get the stateless protocol.
const (
	// protocolVersion is the modern MCP revision this server implements.
	protocolVersion = "2026-07-28"
	// legacyVersion is the handshake-era revision still served for clients
	// that open with initialize.
	legacyVersion = "2025-06-18"
)

// supportedVersions is what server/discover advertises and what an
// UnsupportedProtocolVersionError offers as alternatives, newest first.
func supportedVersions() []string { return []string{protocolVersion, legacyVersion} }

// Reserved _meta keys (MCP 2026-07-28 §General fields).
const (
	metaProtocolVersion = "io.modelcontextprotocol/protocolVersion"
	metaClientInfo      = "io.modelcontextprotocol/clientInfo"
	metaClientCaps      = "io.modelcontextprotocol/clientCapabilities"
	metaLogLevel        = "io.modelcontextprotocol/logLevel"
	metaServerInfo      = "io.modelcontextprotocol/serverInfo"
	metaSubscriptionID  = "io.modelcontextprotocol/subscriptionId"
)

// MCP-defined error codes (-32020..-32099 is reserved for the spec).
const (
	errHeaderMismatch     = -32020
	errMissingClientCap   = -32021
	errUnsupportedVersion = -32022
)

// reqCtx is everything a handler needs to know about the request it is
// answering. It is built fresh per request — nothing is carried over from an
// earlier one, which is what "stateless" means in this revision.
type reqCtx struct {
	modern   bool   // request carried modern per-request metadata
	version  string // protocol version in force for this request
	logLevel string // io.modelcontextprotocol/logLevel, when the client set one
}

// legacyCtx is the context used for handshake-era requests.
func legacyCtx() *reqCtx { return &reqCtx{version: legacyVersion} }

// requestMeta is the parsed params._meta of an incoming request.
type requestMeta struct {
	version       string
	hasVersion    bool
	hasClientCaps bool
	logLevel      string
}

func parseRequestMeta(params json.RawMessage) requestMeta {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil || p.Meta == nil {
		return requestMeta{}
	}
	m := requestMeta{}
	if raw, ok := p.Meta[metaProtocolVersion]; ok {
		var v string
		if json.Unmarshal(raw, &v) == nil && v != "" {
			m.version, m.hasVersion = v, true
		}
	}
	if _, ok := p.Meta[metaClientCaps]; ok {
		m.hasClientCaps = true
	}
	if raw, ok := p.Meta[metaLogLevel]; ok {
		var v string
		if json.Unmarshal(raw, &v) == nil {
			m.logLevel = v
		}
	}
	return m
}

// supportsVersion reports whether we can serve the requested revision.
func supportsVersion(v string) bool {
	for _, s := range supportedVersions() {
		if s == v {
			return true
		}
	}
	return false
}

// modernOnlyMethods are methods that exist only in the stateless revision.
func isModernOnlyMethod(method string) bool {
	switch method {
	case "server/discover", "subscriptions/listen":
		return true
	}
	return false
}

// removedMethods were part of the handshake era and are gone in 2026-07-28.
func isRemovedInModern(method string) bool {
	switch method {
	case "initialize", "notifications/initialized", "ping", "logging/setLevel",
		"resources/subscribe", "resources/unsubscribe":
		return true
	}
	return false
}

// ── result decoration ───────────────────────────────────────────────────────

// cacheHint is the CacheableResult freshness hint for a method's result.
// Servers MUST include ttlMs and cacheScope on the results listed in the spec's
// caching section; every other result carries none.
type cacheHint struct {
	ttlMs      int
	cacheScope string
}

const hourMs = 3600000

// cacheHintFor returns the caching hint for a cacheable method, or ok=false.
// The lists are fixed for the lifetime of the process, so they cache for an
// hour and are safe to share (they contain no user data). resources/read is
// database content: always private, and live diagnoses are never cacheable.
func cacheHintFor(method, uri string) (cacheHint, bool) {
	switch method {
	case "server/discover", "tools/list", "prompts/list", "resources/list", "resources/templates/list":
		return cacheHint{ttlMs: hourMs, cacheScope: "public"}, true
	case "resources/read":
		// health/locks are recomputed on every read by design; handing a client
		// a TTL on them would let it serve a stale verdict.
		if isLiveResource(uri) {
			return cacheHint{ttlMs: 0, cacheScope: "private"}, true
		}
		return cacheHint{ttlMs: 10000, cacheScope: "private"}, true
	}
	return cacheHint{}, false
}

// decorate adds the fields every modern result carries: resultType, the
// server's identity in _meta, and caching hints where the spec requires them.
// Legacy results are returned untouched, byte-for-byte as before.
func (s *server) decorate(rc *reqCtx, method, uri string, result interface{}) interface{} {
	if rc == nil || !rc.modern {
		return result
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		return result
	}
	m["resultType"] = "complete"
	meta, _ := m["_meta"].(map[string]interface{})
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta[metaServerInfo] = map[string]interface{}{"name": "litescope", "version": s.version}
	m["_meta"] = meta
	if h, ok := cacheHintFor(method, uri); ok {
		m["ttlMs"] = h.ttlMs
		m["cacheScope"] = h.cacheScope
	}
	return m
}
