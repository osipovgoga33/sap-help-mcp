package main

import (
	"bufio"
	"encoding/json"
	"io"
)

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
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

var supportedProtocols = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const serverInstructions = `Search two live SAP sources through the single tool sap_help (parameter action).

help.sap.com — official documentation for every SAP product, plus the Support Content space (SAP support FAQs, troubleshooting guides, customizing instructions with links to SAP Notes and KBAs).
community.sap.com — the forum: blogs, Q&A, discussions. Implementation practice and workarounds.

QUERIES MUST BE IN ENGLISH — both sources are English-only. Translate the user's question into SAP terminology rather than word for word ('переоценка валюты' -> 'foreign currency valuation', 'МВЗ' -> 'cost center'). When results are weak, make one or two more calls with different phrasings.

Order of use: action=search -> action=read for the full page; action=community_search -> action=community_read for field experience. Searches return snippets, not answers: read the page or thread before answering. SAP Note numbers found along the way can be opened with a separate SAP Notes server, if one is connected.`

func serve(in io.Reader, out io.Writer, ts *toolset) {
	reader := bufio.NewReaderSize(in, 1<<20)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	for {
		line, err := reader.ReadBytes('\n')
		if len(trimSpaceBytes(line)) > 0 {
			if resp := handle(line, ts); resp != nil {
				_ = enc.Encode(resp)
			}
		}
		if err != nil {
			return
		}
	}
}

func trimSpaceBytes(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\r' || b[j-1] == '\n') {
		j--
	}
	return b[i:j]
}

func handle(line []byte, ts *toolset) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &rpcError{Code: -32700, Message: "parse error"}}
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return nil // уведомление: ответа нет
	}
	resp := &rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := supportedProtocols[0]
		for _, v := range supportedProtocols {
			if v == p.ProtocolVersion {
				version = v
				break
			}
		}
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
			"instructions":    serverInstructions,
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": ts.list()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			return resp
		}
		text, isErr := ts.call(p.Name, p.Arguments)
		resp.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}
