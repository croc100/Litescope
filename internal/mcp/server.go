package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// protocolVersion is the MCP revision this server implements.
const protocolVersion = "2024-11-05"

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
	s := &server{
		tools: tools, byName: byName,
		prompts: prompts, promptByName: promptByName,
		version: version, defaultSource: defaultSource,
	}

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			s.handleLine(line, writer)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *server) handleLine(line []byte, w *bufio.Writer) {
	version := s.version
	tools := s.tools
	byName := s.byName
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
		respond(w, req.ID, map[string]interface{}{
			"protocolVersion": ver,
			"capabilities": map[string]interface{}{
				"tools":     map[string]interface{}{},
				"prompts":   map[string]interface{}{},
				"resources": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{"name": "litescope", "version": version},
		})
	case "notifications/initialized", "notifications/cancelled":
		// notifications: no response
	case "ping":
		respond(w, req.ID, map[string]interface{}{})
	case "tools/list":
		respond(w, req.ID, map[string]interface{}{"tools": toolDescriptors(tools)})
	case "tools/call":
		handleToolCall(w, req, byName)
	case "prompts/list":
		respond(w, req.ID, map[string]interface{}{"prompts": promptDescriptors(s.prompts)})
	case "prompts/get":
		s.handlePromptGet(w, req)
	case "resources/list":
		respond(w, req.ID, map[string]interface{}{"resources": concreteResources(s.defaultSource)})
	case "resources/templates/list":
		respond(w, req.ID, map[string]interface{}{"resourceTemplates": resourceTemplates()})
	case "resources/read":
		handleResourceRead(w, req)
	default:
		if !isNotification {
			respondError(w, req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func handleToolCall(w *bufio.Writer, req rpcRequest, byName map[string]Tool) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		respondError(w, req.ID, -32602, "invalid params")
		return
	}
	tool, ok := byName[params.Name]
	if !ok {
		respondError(w, req.ID, -32602, "unknown tool: "+params.Name)
		return
	}
	text, err := tool.Handler(params.Arguments)
	if err != nil {
		// Tool-level errors are returned in the result with isError, not as a
		// protocol error, so the model can read and react to them.
		respond(w, req.ID, toolResult(fmt.Sprintf("Error: %v", err), true))
		return
	}
	respond(w, req.ID, toolResult(text, false))
}

func (s *server) handlePromptGet(w *bufio.Writer, req rpcRequest) {
	var params struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		respondError(w, req.ID, -32602, "invalid params")
		return
	}
	p, ok := s.promptByName[params.Name]
	if !ok {
		respondError(w, req.ID, -32602, "unknown prompt: "+params.Name)
		return
	}
	for _, a := range p.Arguments {
		if a.Required && params.Arguments[a.Name] == "" {
			respondError(w, req.ID, -32602, "missing required argument: "+a.Name)
			return
		}
	}
	respond(w, req.ID, map[string]interface{}{
		"description": p.Description,
		"messages": []map[string]interface{}{{
			"role":    "user",
			"content": map[string]interface{}{"type": "text", "text": p.Render(params.Arguments)},
		}},
	})
}

func handleResourceRead(w *bufio.Writer, req rpcRequest) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		respondError(w, req.ID, -32602, "invalid params")
		return
	}
	text, mime, err := readResource(params.URI)
	if err != nil {
		respondError(w, req.ID, -32602, err.Error())
		return
	}
	respond(w, req.ID, map[string]interface{}{
		"contents": []map[string]interface{}{{
			"uri":      params.URI,
			"mimeType": mime,
			"text":     text,
		}},
	})
}

func toolDescriptors(tools []Tool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]interface{}{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return out
}

func toolResult(text string, isErr bool) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

func respond(w *bufio.Writer, id json.RawMessage, result interface{}) {
	writeMsg(w, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func respondError(w *bufio.Writer, id json.RawMessage, code int, msg string) {
	writeMsg(w, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func writeMsg(w *bufio.Writer, v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	w.Write(b)
	w.WriteByte('\n')
	w.Flush()
}
