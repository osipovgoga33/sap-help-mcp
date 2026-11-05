package main

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// help.sap.com. Эндпоинты портала не документированы; формы запросов проверены
// на живом портале.
//
// Чтение страницы — три шага, одним запросом полный текст не получить:
//  1. /http.svc/elasticsearch       поиск: loio и url страницы
//  2. /http.svc/deliverableMetadata по loio и продукту: id сборки, buildNo, filePath
//  3. /http.svc/pagecontent         по этим трём: HTML-фрагмент страницы

const (
	helpTranstypes = "standard,html" // только то, что потом можно прочитать; PDF — шум
	maxTextChars   = 12000
)

var docsPathRe = regexp.MustCompile(`/docs/([^/]+)/([^/]+)/([^/?#]+?)\.html`)

func (c *client) helpSearchURL(query, product, version string, limit int) string {
	return buildURL(c.helpBase+"/http.svc/elasticsearch", map[string]string{
		"transtype":            helpTranstypes,
		"state":                "PRODUCTION,TEST,DRAFT",
		"product":              product,
		"version":              version,
		"q":                    query,
		"to":                   strconv.Itoa(clamp(limit, 1, 20) - 1),
		"area":                 "content",
		"advancedSearch":       "0",
		"excludeNotSearchable": "1",
		"language":             "en-US",
	})
}

func (c *client) absoluteHelp(u string) string {
	if u == "" || strings.HasPrefix(u, "http") {
		return u
	}
	if !strings.HasPrefix(u, "/") {
		u = "/" + u
	}
	return c.helpBase + u
}

type helpHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Product string `json:"product,omitempty"`
	Version string `json:"version,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

type helpSearchResult struct {
	Query   string    `json:"query"`
	Found   int       `json:"found"`
	Results []helpHit `json:"results"`
	Source  string    `json:"source,omitempty"`
	Hint    string    `json:"hint,omitempty"`
	Note    string    `json:"note,omitempty"`
}

func (c *client) helpSearch(query, product, version string, limit int) (any, string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, "Empty query."
	}
	limit = clamp(limit, 1, 20)
	data, errText := c.getJSON(c.helpSearchURL(query, product, version, limit), c.helpBase)
	if errText != "" {
		return nil, errText
	}
	hits := getSlice(getMap(data, "data"), "results")
	out := helpSearchResult{Query: query, Results: []helpHit{}}
	for _, h := range hits {
		if len(out.Results) >= limit {
			break
		}
		prod := getStr(h, "product")
		if prod == "" {
			prod = getStr(h, "productId")
		}
		ver := getStr(h, "version")
		if ver == "" {
			ver = getStr(h, "versionId")
		}
		out.Results = append(out.Results, helpHit{
			Title:   strings.TrimSpace(getStr(h, "title")),
			URL:     c.absoluteHelp(getStr(h, "url")),
			Product: prod,
			Version: ver,
			Snippet: stripTags(getStr(h, "snippet")),
		})
	}
	out.Found = len(out.Results)
	if out.Found == 0 {
		out.Note = "The portal found nothing. Try different English terms, or drop the product filter."
		return out, ""
	}
	out.Source = "help.sap.com"
	out.Hint = "Full text: action=read with url. Check the product field — the portal also returns " +
		"ByDesign and cloud editions; for ECC / S/4HANA on premise look for SAP_ERP and SAP_S4HANA_ON_PREMISE."
	return out, ""
}

type pageResult struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	Product    string `json:"product"`
	Version    string `json:"version"`
	Part       int    `json:"part"`
	TotalParts int    `json:"total_parts"`
	More       string `json:"more,omitempty"`
	Source     string `json:"source"`
	Content    string `json:"content"`
}

func (c *client) helpRead(ref string, part int) (any, string) {
	ref = strings.TrimSpace(ref)
	m := docsPathRe.FindStringSubmatch(ref)
	if m == nil {
		return nil, "This does not look like help.sap.com/docs/<product>/<deliverable>/<loio>.html — " +
			"pass the url field returned by action=search."
	}
	productURL, deliverableURL, loio := m[1], m[2], m[3]
	version, language := "", "en-US"
	if pu, err := url.Parse(ref); err == nil {
		q := pu.Query()
		version = q.Get("version")
		if l := q.Get("locale"); l != "" {
			language = l
		}
	}

	meta, errText := c.getJSON(buildURL(c.helpBase+"/http.svc/deliverableMetadata", map[string]string{
		"product_url":                    productURL,
		"topic_url":                      loio + ".html",
		"version":                        version,
		"loadlandingpageontopicnotfound": "true",
		"deliverable_url":                deliverableURL,
		"language":                       language,
		"deliverableInfo":                "1",
		"toc":                            "1",
	}), c.helpBase)
	if errText != "" {
		return nil, errText + " (step: deliverableMetadata)"
	}
	mdata := getMap(meta, "data")
	deliverable := getMap(mdata, "deliverable")
	deliverableID, buildNo := getStr(deliverable, "id"), getStr(deliverable, "buildNo")
	filePath := getStr(mdata, "filePath")
	if filePath == "" {
		filePath = loio + ".html"
	}
	if deliverableID == "" || buildNo == "" {
		return nil, "The portal did not return the page build id (deliverable/buildNo). The version " +
			"may be wrong — retry with a url that has no version parameter."
	}

	page, errText := c.getJSON(buildURL(c.helpBase+"/http.svc/pagecontent", map[string]string{
		"deliverableInfo": "1",
		"deliverable_id":  deliverableID,
		"buildNo":         buildNo,
		"file_path":       filePath,
	}), c.helpBase)
	if errText != "" {
		return nil, errText + " (step: pagecontent)"
	}
	pdata := getMap(page, "data")
	body := getStr(pdata, "body")
	if body == "" {
		return nil, "The page came back empty (no body)."
	}

	chunk, part, total := paginate(htmlToMarkdown(body), part, maxTextChars)
	title := getStr(getMap(pdata, "currentPage"), "t")
	if title == "" {
		title = getStr(deliverable, "title")
	}
	if title == "" {
		title = loio
	}
	ver := getStr(deliverable, "version")
	if ver == "" {
		ver = version
	}
	if ver == "" {
		ver = "latest"
	}
	out := pageResult{Title: title, URL: ref, Product: productURL, Version: ver,
		Part: part, TotalParts: total, Source: "help.sap.com", Content: chunk}
	if total > 1 && part < total {
		out.More = "Part " + strconv.Itoa(part) + " of " + strconv.Itoa(total) +
			"; next: action=read with the same url and part=" + strconv.Itoa(part+1) + "."
	}
	return out, ""
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
