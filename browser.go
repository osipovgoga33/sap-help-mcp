package main

import (
	"encoding/json"
	"strings"
)

// Запасной путь для SAP Community, когда форум отвечает проверкой Cloudflare.
//
// Программа эту проверку пройти не может и не должна пытаться: она привязана к
// настоящему браузеру. Встроенный браузер Claude Desktop — настоящий: человек
// один раз ставит галочку, дальше вкладка community.sap.com ходит в тот же
// API со своими куками. Поэтому сервер в таком случае не выдаёт ошибку, а
// возвращает готовый скрипт: Claude запускает его во встроенном браузере на
// вкладке форума, скрипт делает те же LiQL-запросы, ранжирует и форматирует
// так же, как Go-код, и возвращает компактный JSON.
//
// Запросы LiQL и слова запроса строит Go (одна логика с прямым путём); в
// скрипте — только выборка, слияние, подсчёт совпадений и текст треда.

const communityOrigin = "https://community.sap.com/"

type browserFallback struct {
	Status     string   `json:"status"`
	Reason     string   `json:"reason"`
	Steps      []string `json:"steps"`
	Javascript string   `json:"javascript"`
}

func newFallback(script string) browserFallback {
	return browserFallback{
		Status: "browser_required",
		Reason: "community.sap.com answers this network (usually the VPN exit) with a Cloudflare " +
			"\"verify you are human\" challenge. A program cannot pass it; the built-in browser of " +
			"Claude Desktop can, because it is a real browser.",
		Steps: []string{
			"1. In the built-in browser, open (or reuse) a tab on " + communityOrigin + " and read its title.",
			"2. If the page shows a Cloudflare check (\"Just a moment\", \"verify you are human\" checkbox), " +
				"ask the user to tick it in the browser pane. Never tick it yourself. Wait until the title is \"SAP Community\".",
			"3. Run the script below with the browser's JavaScript tool on that tab, unchanged. It returns the " +
				"same JSON this action returns when the forum is reachable directly.",
			"4. If the script returns an error about HTTP 403, the challenge came back: repeat step 2.",
		},
		Javascript: script,
	}
}

func jsLit(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// jsCommon — разбор ответа LiQL и перевод HTML в текст без DOM (скрипт можно
// проверить в node, а на странице не нужен innerText).
const jsCommon = `
const liql = async (q) => {
  const r = await fetch('/api/2.0/search?q=' + encodeURIComponent(q), {headers: {Accept: 'application/json'}, credentials: 'include'});
  if (!r.ok) throw new Error('HTTP ' + r.status + ' from the Community API');
  const j = await r.json();
  if (j.status !== 'success') throw new Error("The Community API returned status '" + j.status + "'. " + (j.message || ''));
  return (j.data && j.data.items) || [];
};
const kudos = (it) => Number((((it || {}).kudos || {}).sum || {}).weight || 0);
const ENT = {amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' '};
const decode = (s) => s.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (m, e) => {
  if (e[0] === '#') { const n = e[1] === 'x' || e[1] === 'X' ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10); return isNaN(n) ? m : String.fromCodePoint(n); }
  return ENT[e.toLowerCase()] !== undefined ? ENT[e.toLowerCase()] : m;
});
const strip = (s) => decode(String(s || '').replace(/<[^>]+>/g, '')).trim();
const toText = (html) => {
  let s = String(html || '');
  s = s.replace(/<(script|style)\b[^>]*>[\s\S]*?<\/\1\s*>/gi, ' ');
  s = s.replace(/<pre\b[^>]*>([\s\S]*?)<\/pre\s*>/gi, (m, c) => '\n` + "```" + `\n' + c.replace(/<br\s*\/?>/gi, '\n').replace(/<[^>]+>/g, '') + '\n` + "```" + `\n');
  s = s.replace(/<a\b[^>]*href\s*=\s*"(https?:[^"]+)"[^>]*>([\s\S]*?)<\/a\s*>/gi, (m, h, t) => { t = t.replace(/<[^>]+>/g, '').trim(); return t ? '[' + t + '](' + h + ')' : h; });
  s = s.replace(/<br\s*\/?>/gi, '\n').replace(/<li\b[^>]*>/gi, '\n- ').replace(/<\/(p|div|li|h[1-6]|tr|ul|ol|table|blockquote)\s*>/gi, '\n').replace(/<(p|div|h[1-6]|tr|ul|ol|table|blockquote)\b[^>]*>/gi, '\n');
  s = decode(s.replace(/<[^>]+>/g, ''));
  return s.split('\n').map(l => l.replace(/[ \t ]+/g, ' ').trim()).join('\n').replace(/\n{3,}/g, '\n\n').trim();
};
`

// communitySearchJS — тот же поиск, что communitySearch: широкий запрос и
// запросы по редким словам, слияние без дублей, вес 3 за тему и 1 за сниппет,
// отсев шума при трёх и более словах, затем kudos и свежесть.
func communitySearchJS(query string, limit, minKudos int) string {
	terms := queryTerms(query)
	if terms == nil {
		terms = []string{}
	}
	fetch := limit * 3
	if fetch < 25 {
		fetch = 25
	}
	liqls := []string{searchLiql(query, fetch, false, minKudos)}
	for _, t := range rareFirst(terms, 3) {
		liqls = append(liqls, searchLiql(t, fetch, true, minKudos))
	}
	return `await (async () => {
const QUERY = ` + jsLit(query) + `, LIMIT = ` + jsLit(limit) + `, TERMS = ` + jsLit(terms) + `, LIQLS = ` + jsLit(liqls) + `;
` + jsCommon + `
const merged = new Map(); const errors = [];
for (const q of LIQLS) {
  try { for (const it of await liql(q)) { if (it.id != null && !merged.has(String(it.id))) merged.set(String(it.id), it); } }
  catch (e) { errors.push(String(e.message || e)); if (/HTTP 403/.test(String(e))) return {error: String(e.message || e) + ' — the Cloudflare challenge is back; ask the user to tick it in the browser pane, then rerun.'}; }
}
if (!merged.size) {
  if (errors.length) return {error: errors[0]};
  return {query: QUERY, found: 0, results: [], via: 'built-in browser', note: 'Nothing found. The forum matches literally — try the exact error text, a message number, or a transaction name.'};
}
const scored = [];
for (const it of merged.values()) {
  const subj = String(it.subject || '').toLowerCase(), snip = strip(it.search_snippet).toLowerCase();
  let inSubj = 0, inSnip = 0;
  for (const t of TERMS) { if (subj.includes(t)) inSubj++; if (snip.includes(t)) inSnip++; }
  if (TERMS.length >= 3 && inSubj === 0 && inSnip < 2) continue;
  scored.push({score: inSubj * 3 + inSnip, kudos: kudos(it), posted: String(it.post_time || ''), it});
}
scored.sort((a, b) => b.score - a.score || b.kudos - a.kudos || (a.posted < b.posted ? 1 : a.posted > b.posted ? -1 : 0));
const results = scored.slice(0, LIMIT).map(s => {
  const href = String(s.it.view_href || '');
  return {title: String(s.it.subject || '').trim(), url: href.startsWith('http') ? href : 'https://community.sap.com' + href,
    posted: s.posted.slice(0, 10), kudos: s.kudos, match_score: s.score, snippet: strip(s.it.search_snippet)};
});
const out = {query: QUERY, found: results.length, candidates_examined: merged.size, results, source: 'community.sap.com', via: 'built-in browser',
  hint: 'Read promising threads with action=community_read (it will hand back a browser script the same way). This is a forum: verify against documentation or an SAP Note.'};
if (errors.length) out.partial = 'Some sub-queries failed: ' + errors[0];
if (!results.length) out.note = 'There were candidates, but all were dropped as single-common-word matches. Rephrase more precisely.';
return out;
})()`
}

// communityReadJS — тред целиком, как communityRead: повтор без is_solution
// при ошибке синтаксиса, переход к корню треда для ссылки на ответ, нумерация
// ответов, пометки «принятое решение», kudos и глубина, деление на части.
func communityReadJS(messageID string, forum bool, ref string, part int) string {
	return `await (async () => {
const ID = ` + jsLit(messageID) + `, FORUM = ` + jsLit(forum) + `, REF = ` + jsLit(ref) + `, PART = ` + jsLit(part) + `, MAX = ` + jsLit(maxTextChars) + `, LIMIT = ` + jsLit(threadLimit) + `;
const FULL = ` + jsLit(threadLiql("__ID__", true)) + `, MIN = ` + jsLit(threadLiql("__ID__", false)) + `;
` + jsCommon + `
const thread = async (id) => {
  try { return await liql(FULL.split('__ID__').join(id)); }
  catch (e) { if (/syntax/i.test(String(e))) return await liql(MIN.split('__ID__').join(id)); throw e; }
};
let msgs;
try {
  msgs = await thread(ID);
  if (!msgs.length) {
    const r = await liql("SELECT id, conversation.id FROM messages WHERE id = '" + ID + "'");
    const root = r.length && r[0].conversation && r[0].conversation.id;
    if (root && String(root) !== ID) msgs = await thread(String(root));
  }
} catch (e) {
  const m = String(e.message || e);
  return {error: /HTTP 403/.test(m) ? m + ' — the Cloudflare challenge is back; ask the user to tick it in the browser pane, then rerun.' : m};
}
if (!msgs.length) return {error: 'No thread found for message ' + ID + '. The post may have been removed.'};
msgs.sort((a, b) => String(a.post_time || '') < String(b.post_time || '') ? -1 : 1);
const first = FORUM ? 'Question' : 'Article', reply = FORUM ? 'Reply' : 'Comment';
const parts = msgs.map((m, i) => {
  const marks = [];
  if (m.is_solution === true || m.is_solution === 'true') marks.push('accepted solution');
  const k = kudos(m); if (k) marks.push('+' + k);
  const d = Number(m.depth || 0); if (d > 1) marks.push('reply depth ' + d);
  const author = String(((m.author || {}).login) || 'unknown').trim() || 'unknown';
  const head = '## ' + (i ? reply + ' ' + i : first) + ' — ' + author + ', ' + String(m.post_time || '').slice(0, 10) + (marks.length ? ' (' + marks.join(', ') + ')' : '');
  return head + '\n' + (toText(m.body) || '_(empty)_');
});
const text = Array.from(parts.join('\n\n'));
const total = Math.max(1, Math.ceil(text.length / MAX));
const part = Math.min(Math.max(1, PART), total);
const solved = msgs.some(m => m.is_solution === true || m.is_solution === 'true');
const out = {title: String(msgs[0].subject || '').trim(), url: REF.startsWith('http') ? REF : 'https://community.sap.com/t5/-/-/m-p/' + ID,
  kind: FORUM ? 'question' : 'article', replies: msgs.length - 1, solved, part, total_parts: total,
  source: 'community.sap.com', via: 'built-in browser', content: text.slice((part - 1) * MAX, part * MAX).join('')};
if (part < total) out.more = 'Part ' + part + ' of ' + total + '; next: action=community_read with the same url and part=' + (part + 1) + '.';
if (msgs.length >= LIMIT) out.truncated = 'Only the first ' + LIMIT + ' messages of this thread were read; it is longer.';
if (FORUM && !solved && out.replies) out.hint = 'Nothing here is marked as an accepted solution, so treat the replies as suggestions and verify before acting on them.';
return out;
})()`
}

func isCFBlock(errText string) bool { return strings.Contains(errText, cfMarker) }
