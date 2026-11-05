package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// SAP Community через публичный API Khoros (LiQL), без авторизации.
//
// Ловушка, проверенная вживую: MATCHES по фразе из нескольких
// слов работает как ИЛИ по словам и отдаёт результаты без ранжирования, свежие
// первыми. Поэтому:
//  1. отдельные запросы по «редким» словам (FAGL_FCV, длинные слова) — только по теме;
//  2. один широкий запрос по всей фразе — по теме и телу;
//  3. слияние без дублей и своё ранжирование: совпадения в теме (вес 3) и в
//     сниппете (вес 1), затем kudos, затем свежесть. При трёх и более словах
//     посты без совпадения в теме и с менее чем двумя в сниппете отбрасываются.

const (
	liqlSelect          = "SELECT id, subject, search_snippet, post_time, view_href, kudos.sum(weight)"
	threadSelect        = "SELECT id, subject, body, post_time, depth, is_solution, author.login, kudos.sum(weight)"
	threadSelectMinimal = "SELECT id, subject, body, post_time, depth, author.login, kudos.sum(weight)"
	threadLimit         = 100
	maxParallel         = 4
)

var (
	stopWords = map[string]bool{"the": true, "a": true, "an": true, "of": true, "in": true, "on": true,
		"for": true, "to": true, "and": true, "or": true, "is": true, "are": true, "how": true,
		"what": true, "why": true, "with": true, "not": true, "can": true, "sap": true,
		"error": true, "issue": true}
	termSplitRe  = regexp.MustCompile(`[\s,;]+`)
	identLikeRe  = regexp.MustCompile(`[\d_]`)
	messageIDRe  = regexp.MustCompile(`\b(qaq|td|ba|m)-p/(\d+)`)
	digitsOnlyRe = regexp.MustCompile(`^\d+$`)
)

func (c *client) liqlURL(liql string) string {
	return buildURL(c.communityBase+"/api/2.0/search", map[string]string{"q": liql})
}

// liqlEscape экранирует строковый литерал LiQL: сначала обратный слэш, затем кавычку.
func liqlEscape(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `'`, `\'`)
}

func searchLiql(query string, limit int, subjectOnly bool, minKudos int) string {
	e := liqlEscape(query)
	match := "(subject MATCHES '" + e + "' OR body MATCHES '" + e + "')"
	if subjectOnly {
		match = "subject MATCHES '" + e + "'"
	}
	kudos := ""
	if minKudos > 0 {
		kudos = " AND kudos.sum(weight) >= " + strconv.Itoa(minKudos)
	}
	return liqlSelect + " FROM messages WHERE " + match + " AND depth = 0" + kudos +
		" LIMIT " + strconv.Itoa(limit)
}

func (c *client) liqlItems(liql string) ([]any, string) {
	data, errText := c.getJSON(c.liqlURL(liql), "")
	if errText != "" {
		return nil, errText
	}
	if st := getStr(data, "status"); st != "success" {
		msg := getStr(data, "message")
		return nil, "The Community API returned status '" + st + "'. " + msg
	}
	return getSlice(getMap(data, "data"), "items"), ""
}

func queryTerms(q string) []string {
	var out []string
	for _, t := range termSplitRe.Split(strings.ToLower(q), -1) {
		if len([]rune(t)) > 2 && !stopWords[t] {
			out = append(out, t)
		}
	}
	return out
}

// rareFirst — слова, заслуживающие отдельного запроса: сначала похожие на
// идентификаторы (цифры, подчёркивания), затем самые длинные.
func rareFirst(terms []string, keep int) []string {
	var ident, rest []string
	for _, t := range terms {
		if identLikeRe.MatchString(t) {
			ident = append(ident, t)
		} else {
			rest = append(rest, t)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return len(rest[i]) > len(rest[j]) })
	all := append(ident, rest...)
	if len(all) > keep {
		all = all[:keep]
	}
	return all
}

func kudosOf(item any) int {
	return toInt(getMap(getMap(item, "kudos"), "sum")["weight"])
}

type communityHit struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	Posted     string `json:"posted"`
	Kudos      int    `json:"kudos"`
	MatchScore int    `json:"match_score"`
	Snippet    string `json:"snippet,omitempty"`
}

type communitySearchResult struct {
	Query              string         `json:"query"`
	Found              int            `json:"found"`
	CandidatesExamined int            `json:"candidates_examined,omitempty"`
	Results            []communityHit `json:"results"`
	Source             string         `json:"source,omitempty"`
	Hint               string         `json:"hint,omitempty"`
	Partial            string         `json:"partial,omitempty"`
	Note               string         `json:"note,omitempty"`
}

func first10(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func (c *client) communitySearch(query string, limit, minKudos int) (any, string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, "Empty query."
	}
	limit = clamp(limit, 1, 25)
	minKudos = clamp(minKudos, 0, 10)
	terms := queryTerms(query)
	fetch := limit * 3
	if fetch < 25 {
		fetch = 25
	}

	liqls := []string{searchLiql(query, fetch, false, minKudos)}
	for _, t := range rareFirst(terms, 3) {
		liqls = append(liqls, searchLiql(t, fetch, true, minKudos))
	}

	type res struct {
		items []any
		err   string
	}
	results := make([]res, len(liqls))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)
	for i, l := range liqls {
		wg.Add(1)
		go func(i int, l string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			items, e := c.liqlItems(l)
			results[i] = res{items, e}
		}(i, l)
	}
	wg.Wait()

	var errs []string
	merged := map[string]any{}
	var order []string
	for _, r := range results {
		if isCFBlock(r.err) {
			return newFallback(communitySearchJS(query, limit, minKudos)), ""
		}
	}
	for _, r := range results {
		if r.err != "" {
			errs = append(errs, r.err)
			continue
		}
		for _, it := range r.items {
			id := getStr(it, "id")
			if id == "" {
				continue
			}
			if _, seen := merged[id]; !seen {
				merged[id] = it
				order = append(order, id)
			}
		}
	}
	if len(merged) == 0 {
		if len(errs) > 0 {
			return nil, errs[0]
		}
		return communitySearchResult{Query: query, Results: []communityHit{},
			Note: "Nothing found. The forum matches literally — try the exact error text, a message " +
				"number, or a transaction name."}, ""
	}

	type scored struct {
		score, kudos int
		posted       string
		item         any
	}
	var list []scored
	for _, id := range order {
		it := merged[id]
		subject := strings.ToLower(getStr(it, "subject"))
		snippet := strings.ToLower(stripTags(getStr(it, "search_snippet")))
		inSubj, inSnip := 0, 0
		for _, t := range terms {
			if strings.Contains(subject, t) {
				inSubj++
			}
			if strings.Contains(snippet, t) {
				inSnip++
			}
		}
		if len(terms) >= 3 && inSubj == 0 && inSnip < 2 {
			continue // шум: совпало одно частое слово
		}
		list = append(list, scored{inSubj*3 + inSnip, kudosOf(it), getStr(it, "post_time"), it})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		if list[i].kudos != list[j].kudos {
			return list[i].kudos > list[j].kudos
		}
		return list[i].posted > list[j].posted
	})

	out := communitySearchResult{Query: query, CandidatesExamined: len(merged), Results: []communityHit{},
		Source: "community.sap.com"}
	for _, s := range list {
		if len(out.Results) >= limit {
			break
		}
		href := getStr(s.item, "view_href")
		if !strings.HasPrefix(href, "http") {
			href = c.communityBase + href
		}
		out.Results = append(out.Results, communityHit{
			Title:      strings.TrimSpace(getStr(s.item, "subject")),
			URL:        href,
			Posted:     first10(s.posted),
			Kudos:      s.kudos,
			MatchScore: s.score,
			Snippet:    stripTags(getStr(s.item, "search_snippet")),
		})
	}
	out.Found = len(out.Results)
	out.Hint = "Snippets are truncated by the forum — the working answer is usually a few replies down, " +
		"so call action=community_read with the url on anything promising. This is a forum: field " +
		"experience, not the last word — verify against documentation or an SAP Note. match_score " +
		"counts query terms that matched (subject counts more than snippet)."
	if len(errs) > 0 {
		out.Partial = "Some sub-queries failed: " + errs[0]
	}
	if out.Found == 0 {
		out.Note = "There were candidates, but all were dropped as single-common-word matches. " +
			"Rephrase more precisely, or give one or two key terms."
	}
	return out, ""
}

func threadLiql(messageID string, withSolution bool) string {
	sel := threadSelect
	if !withSolution {
		sel = threadSelectMinimal
	}
	return sel + " FROM messages WHERE topic.id = '" + liqlEscape(messageID) +
		"' ORDER BY post_time ASC LIMIT " + strconv.Itoa(threadLimit)
}

// fetchThread — сообщения треда. Если инстанс отвергает is_solution вместе с
// ограничением topic.id, повтор без него: потерять пометку решения лучше, чем тред.
func (c *client) fetchThread(messageID string) ([]any, string) {
	items, e := c.liqlItems(threadLiql(messageID, true))
	if e != "" && strings.Contains(strings.ToLower(e), "syntax") {
		items, e = c.liqlItems(threadLiql(messageID, false))
	}
	return items, e
}

// resolveTopicID — корень треда для ссылки на ответ: topic.id находит только корень.
func (c *client) resolveTopicID(messageID string) (string, string) {
	items, e := c.liqlItems("SELECT id, conversation.id FROM messages WHERE id = '" + liqlEscape(messageID) + "'")
	if e != "" {
		return "", e
	}
	if len(items) == 0 {
		return "", "community.sap.com has no message " + messageID + "."
	}
	return getStr(getMap(items[0], "conversation"), "id"), ""
}

func renderThread(messages []any, forum bool) string {
	firstLabel, replyLabel := "Question", "Reply"
	if !forum {
		firstLabel, replyLabel = "Article", "Comment"
	}
	var b strings.Builder
	for i, m := range messages {
		author := strings.TrimSpace(getStr(getMap(m, "author"), "login"))
		if author == "" {
			author = "unknown"
		}
		var marks []string
		if toBool(getMap1(m)["is_solution"]) {
			marks = append(marks, "accepted solution")
		}
		if k := kudosOf(m); k != 0 {
			marks = append(marks, "+"+strconv.Itoa(k))
		}
		// глубокий ответ обычно отвечает на ответ выше, а не на вопрос
		if d := toInt(getMap1(m)["depth"]); d > 1 {
			marks = append(marks, "reply depth "+strconv.Itoa(d))
		}
		suffix := ""
		if len(marks) > 0 {
			suffix = " (" + strings.Join(marks, ", ") + ")"
		}
		heading := "## " + firstLabel
		if i > 0 {
			heading = "## " + replyLabel + " " + strconv.Itoa(i)
		}
		b.WriteString(heading + " — " + author + ", " + first10(getStr(m, "post_time")) + suffix + "\n")
		body := htmlToMarkdown(getStr(m, "body"))
		if body == "" {
			body = "_(empty)_"
		}
		b.WriteString(body + "\n\n")
	}
	return strings.TrimSpace(b.String())
}

func getMap1(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

type threadResult struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	Kind       string `json:"kind"`
	Replies    int    `json:"replies"`
	Solved     bool   `json:"solved"`
	Part       int    `json:"part"`
	TotalParts int    `json:"total_parts"`
	More       string `json:"more,omitempty"`
	Truncated  string `json:"truncated,omitempty"`
	Hint       string `json:"hint,omitempty"`
	Source     string `json:"source"`
	Content    string `json:"content"`
}

func (c *client) communityRead(ref string, part int) (any, string) {
	ref = strings.TrimSpace(ref)
	marker, messageID := "", ""
	if m := messageIDRe.FindStringSubmatch(ref); m != nil {
		marker, messageID = m[1], m[2]
	} else if digitsOnlyRe.MatchString(ref) {
		messageID = ref
	} else {
		r := []rune(ref)
		if len(r) > 80 {
			r = r[:80]
		}
		return nil, "Pass the url field from an action=community_search result (or a bare message id). " +
			"Expected something ending in -p/<number>, got '" + string(r) + "'."
	}
	// Статьи блога публикуются, а не задаются; message_type у них всё равно
	// «форумный», поэтому надёжен только маркер в URL.
	forum := marker != "ba"

	fallback := func() (any, string) {
		return newFallback(communityReadJS(messageID, forum, ref, part)), ""
	}
	messages, e := c.fetchThread(messageID)
	if isCFBlock(e) {
		return fallback()
	}
	if e != "" {
		return nil, e
	}
	if len(messages) == 0 {
		root, e := c.resolveTopicID(messageID)
		if isCFBlock(e) {
			return fallback()
		}
		if e != "" {
			return nil, e
		}
		if root != "" && root != messageID {
			if messages, e = c.fetchThread(root); isCFBlock(e) {
				return fallback()
			} else if e != "" {
				return nil, e
			}
		}
	}
	if len(messages) == 0 {
		return nil, "No thread found for message " + messageID + ". The post may have been removed, " +
			"or the link may not be a forum message."
	}
	sort.SliceStable(messages, func(i, j int) bool {
		return getStr(messages[i], "post_time") < getStr(messages[j], "post_time")
	})

	solved := false
	for _, m := range messages {
		if toBool(getMap1(m)["is_solution"]) {
			solved = true
		}
	}
	chunk, part, total := paginate(renderThread(messages, forum), part, maxTextChars)
	u := ref
	if !strings.HasPrefix(ref, "http") {
		u = c.communityBase + "/t5/-/-/m-p/" + messageID
	}
	kind := "question"
	if !forum {
		kind = "article"
	}
	out := threadResult{
		Title: strings.TrimSpace(getStr(messages[0], "subject")), URL: u, Kind: kind,
		Replies: len(messages) - 1, Solved: solved, Part: part, TotalParts: total,
		Source: "community.sap.com", Content: chunk,
	}
	if total > 1 && part < total {
		out.More = "Part " + strconv.Itoa(part) + " of " + strconv.Itoa(total) +
			"; next: action=community_read with the same url and part=" + strconv.Itoa(part+1) + "."
	}
	if len(messages) >= threadLimit {
		out.Truncated = "Only the first " + strconv.Itoa(threadLimit) + " messages of this thread were read; it is longer."
	}
	// Без принятого ответа может быть только вопрос с ответами; про статью или
	// тред без ответов такая пометка — шум, похожий на предупреждение.
	if forum && !solved && out.Replies > 0 {
		out.Hint = "Nothing here is marked as an accepted solution, so treat the replies as suggestions " +
			"and verify before acting on them."
	}
	return out, ""
}
