package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Тело сообщения треда 12175476 с community.sap.com (снято
// 2026-08-03): поля, HTML и вложенность — как отдаёт инстанс.
var realThread = []map[string]any{
	{"id": "12175476", "author": map[string]any{"login": "Former Member"},
		"subject": "Foreign Currency Valuation - FAGL_FCV - Reset Valuation Posted without Valuation itself Posted",
		"body": "<P>Hello all,</P>\n  <P>In Foreign Currency Valuation transaction \"FAGL_FCV\" the user executed it " +
			"but by mistake had a check in \"Reset Valuation\".</P>\n  <P>Is this supposed to happen?</P>",
		"post_time": "2020-02-19T17:16:04.000+01:00", "depth": 0, "kudos": map[string]any{"sum": map[string]any{"weight": 0}}},
	{"id": "12175477", "author": map[string]any{"login": "fi_expert"},
		"subject": "Re: Foreign Currency Valuation - FAGL_FCV - Reset Valuation Posted",
		"body": "<P><A href=\"https://wiki.scn.sap.com/wiki/display/ERPFI/Foreign\" rel=\"noopener noreferrer\">Here </A>" +
			"is a good explanation of the reset logic. </P>Also; take a look in table FAGL_BSBW_HISTRY &amp; check which " +
			"transactions were reset.",
		"post_time": "2020-02-20T02:20:50.000+01:00", "depth": 1, "is_solution": true,
		"kudos": map[string]any{"sum": map[string]any{"weight": 1}}},
	{"id": "12175479", "author": map[string]any{"login": "Former Member"},
		"subject":   "Re: Foreign Currency Valuation - FAGL_FCV - Reset Valuation Posted",
		"body":      "<P>Hi,</P><P>Thanks for the feedback.</P>",
		"post_time": "2020-02-20T11:08:49.000+01:00", "depth": 2,
		"kudos": map[string]any{"sum": map[string]any{"weight": 0}}},
}

// Фрагмент страницы help.sap.com 8450d7531a4d424de10000000a174cb4 (SAP_ERP
// 6.18.latest), снят вживую: DITA-инструкция и путь из gif.
const realPageFragment = `<ul class="ul"><li class="li"><p class="p"> ` +
	`<?sap-ot O2O class="- topic/xref " href="0444d7531a4d424de10000000a174cb4.xml"` +
	` text="Foreign currency balance sheet accounts" desc="" xtrc="xref:1"` +
	` xtrf="file:/home/builder/src/dita-all/kid1718795600684/loio17ec785ed229_en-US/src/content/localization/en-us/8450d7531a4d424de10000000a174cb4.xml"` +
	` output-class="xref" outputTopicFile="file:/home/builder/tp.net.sf.dita-ot/2.3/plugins/org.dita.html5/xsl/map2html5Content.xsl" >` +
	"\n, that is, the G/L accounts that you manage in foreign currency.</p></li></ul>" +
	`<p class="p"><span class="ph menucascade">` +
	`<img src="themes/sap-light/img/navstart.gif" alt="Start of the navigation path" title="Start of the navigation path">` +
	`<span class="ph uicontrol">Financial Accounting (New)</span>&nbsp;` +
	`<img src="themes/sap-light/img/navstep.gif" alt="Next navigation step" title="Next navigation step">&nbsp;` +
	`<span class="ph uicontrol">Periodic Processing</span>&nbsp;` +
	`<img src="themes/sap-light/img/navend.gif" alt="End of the navigation path" title="End of the navigation path"></span></p>`

var (
	noisePost = map[string]any{"id": 1, "subject": "Mass update group currency amounts",
		"search_snippet": "currency fields", "post_time": "2026-07-24T10:00:00",
		"view_href": "/t5/x/1", "kudos": map[string]any{"sum": map[string]any{"weight": 0}}}
	targetPost = map[string]any{"id": 2, "subject": "FAGL_FCV valuation of open items in foreign currency",
		"search_snippet": "program <b>FAGL_FCV</b> foreign currency valuation",
		"post_time":      "2020-01-01T10:00:00", "view_href": "/t5/x/2",
		"kudos": map[string]any{"sum": map[string]any{"weight": 4}}}
)

// fakePortal — оба портала в одном httptest-сервере. Запоминает метод и путь
// каждого запроса, чтобы проверять, что наружу уходят только GET.
type fakePortal struct {
	mu       sync.Mutex
	seen     []string
	liql     []string
	thread   []map[string]any
	search   []map[string]any
	liqlFail func(q string) string // непустая строка — ответить status=error
	status   int
	server   *httptest.Server
}

func newFake(t *testing.T) (*fakePortal, *toolset) {
	f := &fakePortal{thread: realThread, search: []map[string]any{noisePost, targetPost}}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	cl := newClient()
	cl.helpBase, cl.communityBase, cl.gap = f.server.URL, f.server.URL, 0
	return f, newToolset(cl)
}

func (f *fakePortal) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte("<html>Access Denied</html>"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	q := r.URL.Query()
	switch r.URL.Path {
	case "/http.svc/elasticsearch":
		write(map[string]any{"data": map[string]any{"results": []any{
			map[string]any{"title": " Foreign Currency Valuation ", "url": "/docs/SAP_ERP/17ec785ed229/8450d7531a4d.html?version=6.18.latest",
				"product": "SAP_ERP", "version": "6.18.latest", "snippet": "Run <b>FAGL_FCV</b> monthly"},
			map[string]any{"title": "ByDesign page", "url": "https://help.sap.com/docs/SAP_BYD/x/y.html", "productId": "SAP_BYD"},
		}}})
	case "/http.svc/deliverableMetadata":
		if q.Get("topic_url") != "8450d7531a4d.html" || q.Get("product_url") != "SAP_ERP" ||
			q.Get("version") != "6.18.latest" {
			w.WriteHeader(400)
			return
		}
		write(map[string]any{"data": map[string]any{"filePath": "8450d7531a4d.html",
			"deliverable": map[string]any{"id": 42, "buildNo": "777", "title": "Deliverable", "version": "6.18.latest"}}})
	case "/http.svc/pagecontent":
		if q.Get("deliverable_id") != "42" || q.Get("buildNo") != "777" {
			w.WriteHeader(400)
			return
		}
		write(map[string]any{"data": map[string]any{"body": realPageFragment,
			"currentPage": map[string]any{"t": "Foreign Currency Valuation"}}})
	case "/api/2.0/search":
		liql := q.Get("q")
		f.mu.Lock()
		f.liql = append(f.liql, liql)
		f.mu.Unlock()
		if f.liqlFail != nil {
			if msg := f.liqlFail(liql); msg != "" {
				write(map[string]any{"status": "error", "message": msg})
				return
			}
		}
		var items []map[string]any
		switch {
		case strings.Contains(liql, "conversation.id"):
			items = []map[string]any{{"id": "12175479", "conversation": map[string]any{"id": "12175476"}}}
		case strings.Contains(liql, "topic.id = '12175476'"):
			items = f.thread
		case strings.Contains(liql, "topic.id"):
			items = nil
		default:
			items = f.search
		}
		write(map[string]any{"status": "success", "data": map[string]any{"items": items}})
	default:
		w.WriteHeader(404)
	}
}

func call(t *testing.T, ts *toolset, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	raw, _ := json.Marshal(args)
	text, isErr := ts.call(toolName, raw)
	var out map[string]any
	if !isErr {
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("ответ не JSON: %v\n%s", err, text)
		}
	}
	return out, text, isErr
}

func (f *fakePortal) onlyGET(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.seen {
		if !strings.HasPrefix(s, "GET ") {
			t.Fatalf("наружу ушёл не GET: %s", s)
		}
	}
}

// --- help.sap.com ---

func TestHelpSearch(t *testing.T) {
	f, ts := newFake(t)
	out, text, isErr := call(t, ts, map[string]any{"action": "search", "query": "foreign currency valuation"})
	if isErr {
		t.Fatal(text)
	}
	res := out["results"].([]any)
	first := res[0].(map[string]any)
	if first["title"] != "Foreign Currency Valuation" || first["product"] != "SAP_ERP" {
		t.Fatalf("первый результат: %v", first)
	}
	if !strings.HasPrefix(first["url"].(string), f.server.URL+"/docs/SAP_ERP/") {
		t.Fatalf("относительный url не стал абсолютным: %v", first["url"])
	}
	if first["snippet"] != "Run FAGL_FCV monthly" {
		t.Fatalf("теги в сниппете: %v", first["snippet"])
	}
	if res[1].(map[string]any)["product"] != "SAP_BYD" {
		t.Fatal("productId не подхвачен как product")
	}
	f.onlyGET(t)
}

func TestHelpSearchURLCarriesPortalParameters(t *testing.T) {
	u := newClient().helpSearchURL("FAGL_FCV", "SAP_ERP", "", 8)
	for _, p := range []string{"transtype=standard%2Chtml", "q=FAGL_FCV", "to=7", "product=SAP_ERP",
		"excludeNotSearchable=1", "language=en-US", "area=content"} {
		if !strings.Contains(u, p) {
			t.Errorf("нет %s в %s", p, u)
		}
	}
	if strings.Contains(u, "version=") {
		t.Error("пустой параметр не отброшен")
	}
}

func TestHelpReadThreeSteps(t *testing.T) {
	f, ts := newFake(t)
	out, text, isErr := call(t, ts, map[string]any{"action": "read",
		"url": "https://help.sap.com/docs/SAP_ERP/17ec785ed229/8450d7531a4d.html?locale=en-US&version=6.18.latest#section-2"})
	if isErr {
		t.Fatal(text)
	}
	if out["title"] != "Foreign Currency Valuation" || out["product"] != "SAP_ERP" || out["version"] != "6.18.latest" {
		t.Fatalf("метаданные: %v", out)
	}
	content := out["content"].(string)
	if !strings.Contains(content, "Foreign currency balance sheet accounts, that is, the G/L accounts") {
		t.Fatalf("подпись DITA-ссылки потеряна:\n%s", content)
	}
	if len(f.seen) != 2 {
		t.Fatalf("ожидалось два запроса (метаданные + страница), было %v", f.seen)
	}
	f.onlyGET(t)
}

func TestHelpReadRejectsForeignURLBeforeNetwork(t *testing.T) {
	f, ts := newFake(t)
	_, text, isErr := call(t, ts, map[string]any{"action": "read", "url": "https://example.com/page"})
	if !isErr || !strings.Contains(text, "help.sap.com/docs") {
		t.Fatalf("ожидалась ошибка формата: %s", text)
	}
	if len(f.seen) != 0 {
		t.Fatalf("запрос ушёл в сеть: %v", f.seen)
	}
}

func TestEmptyQueryNoNetwork(t *testing.T) {
	f, ts := newFake(t)
	for _, a := range []string{"search", "community_search"} {
		if _, _, isErr := call(t, ts, map[string]any{"action": a, "query": "  "}); !isErr {
			t.Fatalf("%s: пустой запрос принят", a)
		}
	}
	if len(f.seen) != 0 {
		t.Fatalf("запрос ушёл в сеть: %v", f.seen)
	}
}

func TestBlockedPortalIsStructuredError(t *testing.T) {
	f, ts := newFake(t)
	f.status = 403
	_, text, isErr := call(t, ts, map[string]any{"action": "search", "query": "x y"})
	if !isErr || !strings.Contains(text, "blocking") || !strings.Contains(text, "do not retry") {
		t.Fatalf("блокировка не распознана: %s", text)
	}
}

func TestHTMLInsteadOfJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>Please enable JavaScript</body></html>"))
	}))
	defer srv.Close()
	cl := newClient()
	cl.helpBase, cl.gap = srv.URL, 0
	_, e := cl.helpSearch("anything", "", "", 5)
	if !strings.Contains(e, "not a JSON object") {
		t.Fatalf("HTML-заглушка не распознана: %s", e)
	}
}

func TestCacheAvoidsSecondRequest(t *testing.T) {
	f, ts := newFake(t)
	call(t, ts, map[string]any{"action": "search", "query": "same query"})
	call(t, ts, map[string]any{"action": "search", "query": "same query"})
	if len(f.seen) != 1 {
		t.Fatalf("повторный запрос не из кэша: %v", f.seen)
	}
}

// --- HTML → markdown ---

func TestPageArtifacts(t *testing.T) {
	md := htmlToMarkdown(realPageFragment)
	for _, leak := range []string{"sap-ot", "/home/builder", "map2html5Content", "outputTopicFile",
		"xtrf", "navstep.gif", "navstart.gif", "navend.gif", "themes/"} {
		if strings.Contains(md, leak) {
			t.Errorf("%q просочилось в текст:\n%s", leak, md)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(md), " "), "Financial Accounting (New) → Periodic Processing") {
		t.Errorf("путь в настройке не читается как путь:\n%s", md)
	}
}

func TestIdentifiersKeepUnderscores(t *testing.T) {
	md := htmlToMarkdown("<p>Check FAGL_BSBW_HISTRY and CX_SY_ZERODIVIDE, then run FAGL_FCV.</p>")
	if !strings.Contains(md, "FAGL_BSBW_HISTRY") || strings.Contains(md, `\_`) {
		t.Fatal(md)
	}
}

func TestScriptsAndStylesDropped(t *testing.T) {
	md := htmlToMarkdown(`<p>Before</p><script>var s=1;alert("boom")</script><style>.a{color:red}</style><p>After</p>`)
	if !strings.Contains(md, "Before") || !strings.Contains(md, "After") ||
		strings.Contains(md, "alert") || strings.Contains(md, "color:red") {
		t.Fatal(md)
	}
}

func TestMarkdownStructure(t *testing.T) {
	md := htmlToMarkdown(`<h2>Procedure</h2><ol><li>Run <code>FAGL_FCV</code></li><li>Check <a href="https://x.y/z">the log</a></li></ol>` +
		`<table><tr><th>Field</th><th>Meaning</th></tr><tr><td>BUKRS</td><td>Company code</td></tr></table>` +
		`<pre>WRITE: / 'a  b'.</pre>`)
	for _, want := range []string{"## Procedure", "1. Run `FAGL_FCV`", "2. Check [the log](https://x.y/z)",
		"| Field | Meaning |", "| --- | --- |", "| BUKRS | Company code |", "```\nWRITE: / 'a  b'.\n```"} {
		if !strings.Contains(md, want) {
			t.Errorf("нет %q в:\n%s", want, md)
		}
	}
}

func TestPaginateKeepsUTF8(t *testing.T) {
	text := strings.Repeat("ж", 25)
	p1, _, total := paginate(text, 1, 10)
	p3, part, _ := paginate(text, 9, 10)
	if total != 3 || p1 != strings.Repeat("ж", 10) || part != 3 || p3 != strings.Repeat("ж", 5) {
		t.Fatalf("%d %q %d %q", total, p1, part, p3)
	}
}

// --- community.sap.com ---

func TestLiqlAssemblyAndEscape(t *testing.T) {
	l := searchLiql("o'reilly \\ test", 25, false, 2)
	for _, want := range []string{`o\'reilly \\ test`, "subject MATCHES", "body MATCHES", "depth = 0",
		"kudos.sum(weight) >= 2", "LIMIT 25"} {
		if !strings.Contains(l, want) {
			t.Errorf("нет %q в %s", want, l)
		}
	}
	if strings.Contains(searchLiql("x", 5, true, 0), "body MATCHES") {
		t.Error("запрос только по теме ищет и в теле")
	}
}

func TestTermsAndRareFirst(t *testing.T) {
	terms := queryTerms("How to fix the SAP error in foreign currency valuation FAGL_FCV")
	got := strings.Join(rareFirst(terms, 3), ",")
	if got != "fagl_fcv,valuation,currency" {
		t.Fatalf("rareFirst: %s (terms %v)", got, terms)
	}
}

func TestRareTermBeatsFreshNoise(t *testing.T) {
	f, ts := newFake(t)
	out, text, isErr := call(t, ts, map[string]any{"action": "community_search",
		"query": "foreign currency valuation FAGL_FCV", "limit": 5})
	if isErr {
		t.Fatal(text)
	}
	res := out["results"].([]any)
	if len(res) == 0 || !strings.Contains(res[0].(map[string]any)["title"].(string), "FAGL_FCV") {
		t.Fatalf("целевой пост не первый: %v", res)
	}
	if res[0].(map[string]any)["url"] != f.server.URL+"/t5/x/2" {
		t.Fatalf("url: %v", res[0])
	}
	// широкий запрос + отдельные по редким словам
	if len(f.liql) != 4 {
		t.Fatalf("ожидалось 4 запроса LiQL, было %d", len(f.liql))
	}
	f.onlyGET(t)
}

func TestSingleCommonWordFiltered(t *testing.T) {
	f, ts := newFake(t)
	f.search = []map[string]any{noisePost}
	out, _, isErr := call(t, ts, map[string]any{"action": "community_search", "query": "asset revaluation index series"})
	if isErr || out["found"].(float64) != 0 || out["note"] == nil {
		t.Fatalf("шум не отфильтрован: %v", out)
	}
}

func TestThreadReadsAsConversation(t *testing.T) {
	_, ts := newFake(t)
	out, text, isErr := call(t, ts, map[string]any{"action": "community_read",
		"url": "https://community.sap.com/t5/erp-q-a/x/qaq-p/12175476"})
	if isErr {
		t.Fatal(text)
	}
	c := out["content"].(string)
	for _, want := range []string{"## Question — Former Member, 2020-02-19",
		"## Reply 1 — fi_expert, 2020-02-20 (accepted solution, +1)", "reply depth 2",
		"FAGL_BSBW_HISTRY", "wiki.scn.sap.com"} {
		if !strings.Contains(c, want) {
			t.Errorf("нет %q в:\n%s", want, c)
		}
	}
	for _, bad := range []string{"<P>", "<A href", "&amp;", "Re: Foreign Currency"} {
		if strings.Contains(c, bad) {
			t.Errorf("%q в тексте треда", bad)
		}
	}
	if out["replies"].(float64) != 2 || out["solved"] != true || out["kind"] != "question" {
		t.Fatalf("метаданные: %v", out)
	}
}

func TestBlogPostIsNotUnsolvedQuestion(t *testing.T) {
	f, ts := newFake(t)
	f.thread = []map[string]any{realThread[0]}
	out, _, _ := call(t, ts, map[string]any{"action": "community_read",
		"url": "https://community.sap.com/t5/erp-blog-posts/x/ba-p/12175476"})
	if out["kind"] != "article" || !strings.HasPrefix(out["content"].(string), "## Article — ") || out["hint"] != nil {
		t.Fatalf("%v", out)
	}
}

func TestUnsolvedThreadSaysSo(t *testing.T) {
	f, ts := newFake(t)
	var unsolved []map[string]any
	for _, m := range realThread {
		c := map[string]any{}
		for k, v := range m {
			c[k] = v
		}
		c["is_solution"] = false
		unsolved = append(unsolved, c)
	}
	f.thread = unsolved
	out, _, _ := call(t, ts, map[string]any{"action": "community_read", "url": "12175476"})
	if out["solved"] != false || !strings.Contains(out["hint"].(string), "accepted solution") {
		t.Fatalf("%v", out)
	}
}

func TestLinkToReplyFallsBackToRoot(t *testing.T) {
	f, ts := newFake(t)
	out, text, isErr := call(t, ts, map[string]any{"action": "community_read",
		"url": "https://community.sap.com/t5/x/y/m-p/12175479"})
	if isErr {
		t.Fatal(text)
	}
	if out["replies"].(float64) != 2 || len(f.liql) != 3 {
		t.Fatalf("корень не найден: %v, запросов %d", out, len(f.liql))
	}
}

func TestSyntaxErrorRetriesWithoutSolutionFlag(t *testing.T) {
	f, ts := newFake(t)
	f.liqlFail = func(q string) string {
		if strings.Contains(q, "is_solution") {
			return "Invalid query syntax"
		}
		return ""
	}
	out, text, isErr := call(t, ts, map[string]any{"action": "community_read", "url": "12175476"})
	if isErr || out["replies"].(float64) != 2 || len(f.liql) != 2 {
		t.Fatalf("%s / запросов %d", text, len(f.liql))
	}
}

func TestLongThreadIsPaginated(t *testing.T) {
	f, ts := newFake(t)
	filler := map[string]any{}
	for k, v := range realThread[1] {
		filler[k] = v
	}
	filler["body"] = "<P>" + strings.Repeat("word ", 4000) + "</P>"
	f.thread = []map[string]any{realThread[0], filler}
	first, _, _ := call(t, ts, map[string]any{"action": "community_read", "url": "12175476"})
	second, _, _ := call(t, ts, map[string]any{"action": "community_read", "url": "12175476", "part": 2})
	if first["total_parts"].(float64) < 2 || !strings.Contains(first["more"].(string), "part=2") ||
		second["part"].(float64) != 2 || second["content"] == first["content"] {
		t.Fatalf("пагинация: %v / %v", first["more"], second["part"])
	}
}

func TestUnusableRefRejectedBeforeNetwork(t *testing.T) {
	f, ts := newFake(t)
	_, text, isErr := call(t, ts, map[string]any{"action": "community_read", "url": "not a link"})
	if !isErr || !strings.Contains(text, "-p/<number>") || len(f.seen) != 0 {
		t.Fatalf("%s / %v", text, f.seen)
	}
}

// --- инструмент и протокол ---

func TestToolArgumentsValidated(t *testing.T) {
	_, ts := newFake(t)
	cases := map[string]string{
		`{"action":"bogus"}`:                "Unknown action",
		`{}`:                                "Pass action",
		`{"action":"search","quer":"typo"}`: "Invalid arguments",
		`{"action":"read"}`:                 "Pass url",
	}
	for args, want := range cases {
		text, isErr := ts.call(toolName, json.RawMessage(args))
		if !isErr || !strings.Contains(text, want) {
			t.Errorf("%s: %s", args, text)
		}
	}
	if text, isErr := ts.call("other", nil); !isErr || !strings.Contains(text, "one tool") {
		t.Error(text)
	}
}

func TestProtocol(t *testing.T) {
	_, ts := newFake(t)
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"sap_help","arguments":{"action":"status"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`,
		`not json`,
	}, "\n") + "\n"
	var out bytes.Buffer
	serve(strings.NewReader(in), &out, ts)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("ожидалось 5 ответов (уведомление без ответа), было %d:\n%s", len(lines), out.String())
	}
	var r [5]map[string]any
	for i, l := range lines {
		if err := json.Unmarshal([]byte(l), &r[i]); err != nil {
			t.Fatalf("строка %d не JSON: %s", i, l)
		}
	}
	init := r[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" ||
		init["serverInfo"].(map[string]any)["version"] != serverVersion {
		t.Fatalf("initialize: %v", init)
	}
	tools := r[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != toolName {
		t.Fatalf("ожидался один инструмент: %v", tools)
	}
	call := r[2]["result"].(map[string]any)
	if call["isError"] != false || !strings.Contains(call["content"].([]any)[0].(map[string]any)["text"].(string), serverVersion) {
		t.Fatalf("status: %v", call)
	}
	if r[3]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("неизвестный метод: %v", r[3])
	}
	if r[4]["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Fatalf("мусор на входе: %v", r[4])
	}
}

func TestUnknownProtocolVersionGetsLatest(t *testing.T) {
	_, ts := newFake(t)
	resp := handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`), ts)
	if resp.Result.(map[string]any)["protocolVersion"] != supportedProtocols[0] {
		t.Fatal(resp.Result)
	}
}

func TestListItemWithParagraph(t *testing.T) {
	md := htmlToMarkdown(`<ul><li><p>First</p></li><li><p>Second</p></li></ul>`)
	if md != "- First\n- Second" {
		t.Fatalf("%q", md)
	}
}

// TLS-сервер, который умеет HTTP/2, должен видеть от клиента только HTTP/1.1.
// В 0.1.1 клиент заявлял h2 и падал на кадре SETTINGS («malformed HTTP response»).
func TestClientSpeaksHTTP1EvenIfServerOffersH2(t *testing.T) {
	var proto string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto = r.Proto
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"items":[]}}`))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	cl := newClient()
	cl.gap = 0
	tr := cl.http.Transport.(*http.Transport)
	tr.TLSClientConfig.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	if _, e := cl.getJSON(srv.URL+"/x", ""); e != "" {
		t.Fatal(e)
	}
	if proto != "HTTP/1.1" {
		t.Fatalf("клиент пришёл по %s", proto)
	}
}

// --- форум за проверкой Cloudflare → встроенный браузер ---

func cloudflareFake(t *testing.T) (*atomic.Int32, *toolset) {
	hits := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Server", "cloudflare")
		w.Header().Set("Cf-Ray", "a55c30110ec1fa44-ARN")
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>Just a moment...</title></head><body><script src="/cdn-cgi/challenge-platform/x"></script></body></html>`))
	}))
	t.Cleanup(srv.Close)
	cl := newClient()
	cl.communityBase, cl.gap = srv.URL, 0
	return hits, newToolset(cl)
}

func TestCloudflareGivesBrowserScriptAndHoldsHost(t *testing.T) {
	hits, ts := cloudflareFake(t)
	out, text, isErr := call(t, ts, map[string]any{"action": "community_search", "query": "FAGL_FCV reset valuation"})
	if isErr || out["status"] != "browser_required" {
		t.Fatalf("ожидался browser_required: %s", text)
	}
	js := out["javascript"].(string)
	for _, want := range []string{"/api/2.0/search", "FAGL_FCV reset valuation", "subject MATCHES 'fagl_fcv'"} {
		if !strings.Contains(js, want) {
			t.Errorf("в скрипте нет %q", want)
		}
	}
	if !strings.Contains(text, "Never tick it yourself") {
		t.Error("нет запрета ставить галочку самому")
	}
	first := hits.Load()
	// повторный вызов и чтение треда — без сети, сразу скрипт
	out2, _, _ := call(t, ts, map[string]any{"action": "community_read", "url": "https://community.sap.com/t5/x/y/qaq-p/12175476"})
	if out2["status"] != "browser_required" || !strings.Contains(out2["javascript"].(string), "topic.id = '__ID__'") {
		t.Fatalf("чтение: %v", out2)
	}
	if hits.Load() != first {
		t.Fatalf("после проверки Cloudflare были ещё запросы: %d → %d", first, hits.Load())
	}
}

func TestPlain403IsNotCloudflare(t *testing.T) {
	f, ts := newFake(t)
	f.status = 403
	_, text, isErr := call(t, ts, map[string]any{"action": "community_search", "query": "x y"})
	if !isErr || strings.Contains(text, "browser_required") {
		t.Fatalf("обычный 403 принят за Cloudflare: %s", text)
	}
}

// Скрипты прогоняются в node с поддельным fetch на тех же образцах форума.
func runJS(t *testing.T, script string, liqlItems func(q string) any) map[string]any {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден")
	}
	fixtures, _ := json.Marshal(map[string]any{
		"thread": realThread, "search": []map[string]any{noisePost, targetPost},
	})
	harness := `
const FX = ` + string(fixtures) + `;
const calls = [];
globalThis.fetch = async (url) => {
  const q = decodeURIComponent(url.split('q=')[1]);
  calls.push(q);
  let items;
  if (q.includes('conversation.id')) items = [{id: '12175479', conversation: {id: '12175476'}}];
  else if (q.includes("topic.id = '12175476'")) items = FX.thread;
  else if (q.includes('topic.id')) items = [];
  else items = FX.search;
  return {ok: true, status: 200, json: async () => ({status: 'success', data: {items}})};
};
(async () => { const r = ` + script + `; r.__calls = calls.length; console.log(JSON.stringify(r)); })().catch(e => { console.log(JSON.stringify({__throw: String(e)})); });
`
	dir := t.TempDir()
	path := filepath.Join(dir, "h.mjs")
	if err := os.WriteFile(path, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	outB, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, outB)
	}
	var out map[string]any
	if err := json.Unmarshal(outB, &out); err != nil {
		t.Fatalf("не JSON: %s", outB)
	}
	if out["__throw"] != nil {
		t.Fatalf("скрипт упал: %v", out["__throw"])
	}
	return out
}

func TestBrowserSearchScriptRanksLikeGo(t *testing.T) {
	out := runJS(t, communitySearchJS("foreign currency valuation FAGL_FCV", 5, 0), nil)
	res := out["results"].([]any)
	if len(res) == 0 || !strings.Contains(res[0].(map[string]any)["title"].(string), "FAGL_FCV") {
		t.Fatalf("целевой пост не первый: %v", out)
	}
	if res[0].(map[string]any)["url"] != "https://community.sap.com/t5/x/2" ||
		res[0].(map[string]any)["snippet"] != "program FAGL_FCV foreign currency valuation" {
		t.Fatalf("%v", res[0])
	}
	if out["__calls"].(float64) != 4 || out["via"] != "built-in browser" {
		t.Fatalf("%v", out)
	}
}

func TestBrowserSearchScriptFiltersNoise(t *testing.T) {
	out := runJS(t, communitySearchJS("asset revaluation index series", 5, 0), nil)
	// в образцах нет постов про asset revaluation — оба отсеиваются
	if out["found"].(float64) != 0 || out["note"] == nil {
		t.Fatalf("%v", out)
	}
}

func TestBrowserReadScript(t *testing.T) {
	out := runJS(t, communityReadJS("12175479", true, "https://community.sap.com/t5/x/y/m-p/12175479", 1), nil)
	c := out["content"].(string)
	for _, want := range []string{"## Question — Former Member, 2020-02-19",
		"## Reply 1 — fi_expert, 2020-02-20 (accepted solution, +1)", "reply depth 2",
		"FAGL_BSBW_HISTRY & check", "[Here](https://wiki.scn.sap.com/wiki/display/ERPFI/Foreign)"} {
		if !strings.Contains(c, want) {
			t.Errorf("нет %q в:\n%s", want, c)
		}
	}
	if strings.Contains(c, "<P>") || strings.Contains(c, "&amp;") || strings.Contains(c, "Re: Foreign") {
		t.Errorf("мусор в тексте:\n%s", c)
	}
	// ссылка на ответ: пусто → поиск корня → тред
	if out["replies"].(float64) != 2 || out["solved"] != true || out["__calls"].(float64) != 3 {
		t.Fatalf("%v", out)
	}
}

func TestBrowserReadScriptArticle(t *testing.T) {
	out := runJS(t, communityReadJS("12175476", false, "https://community.sap.com/t5/b/x/ba-p/12175476", 1), nil)
	if out["kind"] != "article" || !strings.HasPrefix(out["content"].(string), "## Article — ") {
		t.Fatalf("%v", out)
	}
	if !strings.Contains(out["content"].(string), "## Comment 1 — ") {
		t.Fatal("комментарии статьи названы ответами")
	}
}
