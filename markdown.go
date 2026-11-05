package main

import (
	"html"
	"regexp"
	"strings"
	"unicode"
)

// Перевод HTML-фрагментов порталов в markdown. Внешнего парсера нет намеренно:
// фрагменты небольшие и предсказуемые (страница help.sap.com, тело сообщения
// форума), и на них хватает простого сканера тегов.

var (
	scriptRe  = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	styleRe   = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

	// Остатки DITA-сборки портала: <?sap-ot ... text="Подпись ссылки" ... >,
	// закрываются обычным ">", а не "?>". Видимая подпись ссылки живёт ТОЛЬКО в
	// атрибуте text=, поэтому инструкцию надо схлопнуть в подпись, а не удалить:
	// иначе из фразы пропадает подлежащее. \s* в конце съедает перевод строки,
	// который портал ставит сразу после инструкции и который рвёт предложение.
	piRe     = regexp.MustCompile(`(?s)<\?[^>]*>\s*`)
	piTextRe = regexp.MustCompile(`\btext="([^"]*)"`)

	// Путь в настройке рисуется как подпись/gif/подпись/gif; navstep — разделитель.
	navImgRe = regexp.MustCompile(`(?i)<img\b[^>]*?\bnav(start|step|end)\.gif\b[^>]*>`)

	tagHTMLRe        = regexp.MustCompile(`<[^>]+>`)
	attrRe           = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
	manyNLRe         = regexp.MustCompile(`\n{3,}`)
	listMarkerLineRe = regexp.MustCompile(`(^|\n)[ ]*(-|\d+\.) $`)
	trailingSpR      = regexp.MustCompile(`[ \t]+\n`)
)

const noSpaceBefore = ",.;:!?)]}"

func stripPortalArtifacts(s string) string {
	s = scriptRe.ReplaceAllString(s, " ")
	s = styleRe.ReplaceAllString(s, " ")
	s = commentRe.ReplaceAllString(s, " ")
	s = navImgRe.ReplaceAllStringFunc(s, func(m string) string {
		switch strings.ToLower(navImgRe.FindStringSubmatch(m)[1]) {
		case "step":
			return " → "
		default:
			return ""
		}
	})

	var b strings.Builder
	last := 0
	for _, loc := range piRe.FindAllStringIndex(s, -1) {
		b.WriteString(s[last:loc[0]])
		m := s[loc[0]:loc[1]]
		if t := piTextRe.FindStringSubmatch(m); t != nil {
			label := html.UnescapeString(t[1])
			if loc[1] < len(s) && !strings.ContainsRune(noSpaceBefore, rune(s[loc[1]])) {
				label += " "
			}
			// Подпись вставляется как текст: экранируем, чтобы сканер тегов
			// не принял её за разметку.
			b.WriteString(html.EscapeString(label))
		} else {
			b.WriteString(" ")
		}
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// stripTags — плоский текст для сниппетов поиска.
func stripTags(s string) string {
	s = tagHTMLRe.ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}

type listState struct {
	ordered bool
	n       int
}

type mdWriter struct {
	b       strings.Builder
	lists   []listState
	pre     int
	links   []string
	cell    int  // номер ячейки в текущей строке таблицы
	header  bool // в текущей строке были <th>
	rowNo   int  // номер строки в текущей таблице
	cellCnt int  // число ячеек в первой строке
}

func (w *mdWriter) str() string { return w.b.String() }

// trailingNewlines — сколько переводов строки стоит в конце вывода.
func (w *mdWriter) trailingNewlines() int {
	s := w.str()
	n := 0
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case '\n':
			n++
		case ' ', '\t':
			continue
		default:
			return n
		}
	}
	if len(s) == 0 {
		return 2 // начало документа — как будто после абзаца
	}
	return n
}

func (w *mdWriter) ensureNewlines(n int) {
	// Абзац сразу после маркера пункта списка (<li><p>…) не должен отрывать
	// текст от маркера.
	if listMarkerLineRe.MatchString(w.str()) {
		return
	}
	// срезаем хвостовые пробелы перед переводом строки
	s := strings.TrimRight(w.str(), " \t")
	w.b.Reset()
	w.b.WriteString(s)
	for have := w.trailingNewlines(); have < n; have++ {
		w.b.WriteByte('\n')
	}
}

func (w *mdWriter) atLineStart() bool {
	s := w.str()
	return len(s) == 0 || s[len(s)-1] == '\n'
}

func (w *mdWriter) text(t string) {
	t = html.UnescapeString(t)
	if w.pre > 0 {
		w.b.WriteString(t)
		return
	}
	var b strings.Builder
	space := false
	for _, r := range t {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	if space {
		b.WriteByte(' ')
	}
	out := b.String()
	if out == "" {
		return
	}
	s := w.str()
	if w.atLineStart() || strings.HasSuffix(s, " ") {
		out = strings.TrimLeft(out, " ")
	}
	w.b.WriteString(out)
}

func (w *mdWriter) raw(s string) { w.b.WriteString(s) }

func parseAttrs(tag string) map[string]string {
	attrs := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
		v := m[3]
		if v == "" {
			v = m[4]
		}
		if v == "" {
			v = m[5]
		}
		attrs[strings.ToLower(m[1])] = html.UnescapeString(v)
	}
	return attrs
}

func usableHref(h string) bool {
	h = strings.TrimSpace(h)
	return strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://")
}

// htmlToMarkdown переводит HTML-фрагмент в markdown. Подчёркивания и звёздочки
// не экранируются: почти все идентификаторы SAP — FAGL_BSBW_HISTRY, CX_SY_* —
// с экранированием дошли бы до модели как FAGL\_BSBW\_HISTRY.
func htmlToMarkdown(src string) string {
	src = stripPortalArtifacts(src)
	w := &mdWriter{}
	i := 0
	for i < len(src) {
		lt := strings.IndexByte(src[i:], '<')
		if lt < 0 {
			w.text(src[i:])
			break
		}
		if lt > 0 {
			w.text(src[i : i+lt])
		}
		i += lt
		gt := strings.IndexByte(src[i:], '>')
		if gt < 0 {
			w.text(src[i:])
			break
		}
		tag := src[i : i+gt+1]
		i += gt + 1
		w.tag(tag)
	}
	out := w.str()
	out = trailingSpR.ReplaceAllString(out, "\n")
	out = manyNLRe.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

func (w *mdWriter) tag(tag string) {
	inner := strings.TrimSpace(tag[1 : len(tag)-1])
	if inner == "" || inner[0] == '!' || inner[0] == '?' {
		return
	}
	closing := inner[0] == '/'
	if closing {
		inner = strings.TrimSpace(inner[1:])
	}
	name := inner
	if k := strings.IndexFunc(inner, func(r rune) bool { return unicode.IsSpace(r) || r == '/' }); k >= 0 {
		name = inner[:k]
	}
	name = strings.ToLower(name)

	if w.pre > 0 && name != "pre" {
		return // внутри <pre> разметку не интерпретируем, только текст
	}

	switch name {
	case "p", "div", "section", "article", "header", "footer", "blockquote", "dl", "figure":
		if len(w.lists) > 0 {
			w.ensureNewlines(1) // внутри списка абзацы не рвут его пустыми строками
		} else {
			w.ensureNewlines(2)
		}
	case "dt", "dd":
		w.ensureNewlines(1)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		w.ensureNewlines(2)
		if !closing {
			w.raw(strings.Repeat("#", int(name[1]-'0')) + " ")
		}
	case "br":
		w.raw("\n")
	case "hr":
		w.ensureNewlines(2)
		w.raw("---")
		w.ensureNewlines(2)
	case "ul", "ol":
		if closing {
			if len(w.lists) > 0 {
				w.lists = w.lists[:len(w.lists)-1]
			}
			if len(w.lists) == 0 {
				w.ensureNewlines(2)
			} else {
				w.ensureNewlines(1)
			}
		} else {
			if len(w.lists) == 0 {
				w.ensureNewlines(2)
			} else {
				w.ensureNewlines(1)
			}
			w.lists = append(w.lists, listState{ordered: name == "ol"})
		}
	case "li":
		if closing {
			w.ensureNewlines(1)
			return
		}
		w.ensureNewlines(1)
		depth := len(w.lists)
		if depth == 0 {
			depth = 1
			w.lists = append(w.lists, listState{})
		}
		st := &w.lists[len(w.lists)-1]
		w.raw(strings.Repeat("  ", depth-1))
		if st.ordered {
			st.n++
			w.raw(itoa(st.n) + ". ")
		} else {
			w.raw("- ")
		}
	case "a":
		if closing {
			if len(w.links) > 0 {
				href := w.links[len(w.links)-1]
				w.links = w.links[:len(w.links)-1]
				if href != "" {
					s := strings.TrimRight(w.str(), " ")
					w.b.Reset()
					w.b.WriteString(s)
					w.raw("](" + href + ") ")
				}
			}
			return
		}
		href := parseAttrs(inner)["href"]
		if usableHref(href) {
			w.text(" ")
			w.raw("[")
			w.links = append(w.links, strings.TrimSpace(href))
		} else {
			w.links = append(w.links, "")
		}
	case "strong", "b":
		w.raw("**")
	case "em", "i":
		w.raw("*")
	case "code", "tt", "kbd":
		if w.pre == 0 {
			w.raw("`")
		}
	case "pre":
		if closing {
			if w.pre > 0 {
				w.pre--
			}
			w.ensureNewlines(1)
			w.raw("```")
			w.ensureNewlines(2)
		} else {
			w.ensureNewlines(2)
			w.raw("```\n")
			w.pre++
		}
	case "table":
		w.ensureNewlines(2)
		w.rowNo, w.cellCnt = 0, 0
	case "tr":
		if closing {
			w.raw(" |")
			w.ensureNewlines(1)
			if w.rowNo == 0 {
				w.cellCnt = w.cell
				// разделитель после первой строки: markdown-таблица без него не таблица
				w.raw("|" + strings.Repeat(" --- |", maxInt(w.cell, 1)))
				w.ensureNewlines(1)
			}
			w.rowNo++
			return
		}
		w.ensureNewlines(1)
		w.cell, w.header = 0, false
		w.raw("|")
	case "td", "th":
		if closing {
			return
		}
		if name == "th" {
			w.header = true
		}
		if w.cell > 0 {
			w.raw(" |")
		}
		w.raw(" ")
		w.cell++
	}
	// <img> и прочее не выводятся: картинка модели не видна, а её подпись на
	// порталах — служебный шум («Start of the navigation path»).
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// paginate режет текст на части по maxChars символов (не байт: UTF-8 не рвётся).
func paginate(text string, part, maxChars int) (string, int, int) {
	r := []rune(text)
	total := (len(r) + maxChars - 1) / maxChars
	if total < 1 {
		total = 1
	}
	if part < 1 {
		part = 1
	}
	if part > total {
		part = total
	}
	from := (part - 1) * maxChars
	to := from + maxChars
	if to > len(r) {
		to = len(r)
	}
	return string(r[from:to]), part, total
}
