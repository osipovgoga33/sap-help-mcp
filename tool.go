package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const toolName = "sap_help"

const toolDescription = `Live search and reading of SAP documentation (help.sap.com) and SAP Community (community.sap.com). Read-only, no credentials.

Call this FIRST — before a web search and before answering from memory — for any question about standard SAP ERP / S/4HANA: an error or message code, how to configure something, a customizing path, the behaviour of a transaction or report.

QUERIES IN ENGLISH, in SAP terminology (translate the user's question yourself).

Actions:
- search: help.sap.com — documentation and the Support Content wiki (SAP support FAQs and troubleshooting guides). Params: query (required), product (optional: SAP_ERP = ECC 6.0, SAP_S4HANA_ON_PREMISE, SAP_S4HANA_CLOUD, SUPPORT_CONTENT; without it ByDesign and cloud editions come back too — check the product field), version (optional, exactly as a previous result reported it, e.g. 6.18.latest), limit (1-20, default 8).
- read: full text of a help.sap.com page as markdown. Params: url (the url field of a search result), part (for long pages, see total_parts).
- community_search: SAP Community blogs, Q&A, discussions — field experience and workarounds, not the last word. Params: query (required; exact error text, message number or transaction works best), limit (1-25, default 8), min_kudos (0-10: 0 everything, 1-2 drop unanswered, 5+ community-validated only).
- community_read: a whole thread — the question and every reply in order, with authors, kudos and the accepted solution marked. Params: url (the url field of a community_search result, or a bare message id), part.
- status: server version, endpoints, cache state.

Searches return snippets, not answers: read the page or thread before answering, and give the user the url.

If community_search or community_read returns status "browser_required", the forum is behind a Cloudflare check for this network: follow its steps — open community.sap.com in the built-in browser, let the user tick the check if one appears (never tick it yourself), then run the returned javascript unchanged on that tab. The script returns the same JSON. help.sap.com is not affected.`

type toolArgs struct {
	Action   string `json:"action"`
	Query    string `json:"query"`
	Product  string `json:"product"`
	Version  string `json:"version"`
	Limit    int    `json:"limit"`
	MinKudos int    `json:"min_kudos"`
	URL      string `json:"url"`
	Part     int    `json:"part"`
}

type toolset struct {
	cl *client
}

func newToolset(cl *client) *toolset { return &toolset{cl: cl} }

func (ts *toolset) list() []map[string]any {
	return []map[string]any{{
		"name":        toolName,
		"title":       "SAP Help and Community",
		"description": toolDescription,
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string",
					"enum": []string{"search", "read", "community_search", "community_read", "status"}},
				"query":     map[string]any{"type": "string", "description": "Search query, in English."},
				"product":   map[string]any{"type": "string", "description": "search: product line filter."},
				"version":   map[string]any{"type": "string", "description": "search: product version, as a previous result reported it."},
				"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 25, "description": "How many results (default 8)."},
				"min_kudos": map[string]any{"type": "integer", "minimum": 0, "maximum": 10, "description": "community_search: minimum kudos."},
				"url":       map[string]any{"type": "string", "description": "read / community_read: url of a search result."},
				"part":      map[string]any{"type": "integer", "minimum": 1, "description": "read / community_read: part number of a long text."},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
		"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": true},
	}}
}

func (ts *toolset) call(name string, raw json.RawMessage) (string, bool) {
	if name != toolName {
		return "Unknown tool: " + name + ". This server has one tool, " + toolName + ".", true
	}
	var a toolArgs
	if len(raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&a); err != nil {
			return "Invalid arguments: " + err.Error(), true
		}
	}
	if a.Limit == 0 {
		a.Limit = 8
	}
	if a.Part == 0 {
		a.Part = 1
	}

	var res any
	var errText string
	switch strings.TrimSpace(a.Action) {
	case "search":
		res, errText = ts.cl.helpSearch(a.Query, a.Product, a.Version, a.Limit)
	case "read":
		if a.URL == "" {
			return "Pass url — the url field of an action=search result.", true
		}
		res, errText = ts.cl.helpRead(a.URL, a.Part)
	case "community_search":
		res, errText = ts.cl.communitySearch(a.Query, a.Limit, a.MinKudos)
	case "community_read":
		if a.URL == "" {
			return "Pass url — the url field of an action=community_search result, or a bare message id.", true
		}
		res, errText = ts.cl.communityRead(a.URL, a.Part)
	case "status":
		res = map[string]any{
			"server":             serverName,
			"version":            serverVersion,
			"search_endpoint":    ts.cl.helpBase + "/http.svc/elasticsearch",
			"read_endpoints":     []string{ts.cl.helpBase + "/http.svc/deliverableMetadata", ts.cl.helpBase + "/http.svc/pagecontent"},
			"community_endpoint": ts.cl.communityBase + "/api/2.0/search",
			"cache":              map[string]any{"entries": ts.cl.cacheSize(), "ttl_hours": int(cacheTTL.Hours())},
			"note":               "The help.sap.com endpoints are not publicly documented and may change.",
		}
	case "":
		return "Pass action: search, read, community_search, community_read or status.", true
	default:
		return fmt.Sprintf("Unknown action %q. Use search, read, community_search, community_read or status.", a.Action), true
	}
	if errText != "" {
		return errText, true
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(res); err != nil {
		return "Failed to encode the result: " + err.Error(), true
	}
	return strings.TrimSpace(buf.String()), false
}
