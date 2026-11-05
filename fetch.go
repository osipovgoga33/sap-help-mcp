package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	requestTimeout = 12 * time.Second
	cacheTTL       = 6 * time.Hour
	cacheMax       = 300
	// Пауза между запросами к порталу. Порталы публичные и без учётки, но
	// серия из десятка запросов в одну секунду выглядит как выкачка.
	minGap       = 200 * time.Millisecond
	maxBodyBytes = 8 << 20
)

type cacheEntry struct {
	at   time.Time
	data map[string]any
}

type client struct {
	helpBase      string
	communityBase string
	http          *http.Client
	userAgent     string

	mu    sync.Mutex
	cache map[string]cacheEntry

	gapMu sync.Mutex
	last  time.Time
	gap   time.Duration

	// Хост, закрытый проверкой Cloudflare, → до какого времени туда не ходить.
	blockMu      sync.Mutex
	blockedUntil map[string]time.Time
}

// cfMarker — признак в тексте ошибки: хост закрыт проверкой Cloudflare.
// Действия форума по нему переключаются на встроенный браузер.
const cfMarker = "[cloudflare-challenge]"

// cfBlockHold — сколько не стучаться в хост после проверки Cloudflare:
// повторные запросы проверку не снимут, а репутацию адреса ухудшат.
const cfBlockHold = 30 * time.Minute

func isCloudflareChallenge(resp *http.Response, body []byte) bool {
	if resp.Header.Get("Cf-Mitigated") == "challenge" {
		return true
	}
	if !strings.EqualFold(resp.Header.Get("Server"), "cloudflare") {
		return false
	}
	b := string(body)
	return strings.Contains(b, "Just a moment") || strings.Contains(b, "challenge-platform") ||
		strings.Contains(b, "cf-chl") || strings.Contains(b, "Один момент")
}

func (c *client) hostBlocked(host string) bool {
	c.blockMu.Lock()
	defer c.blockMu.Unlock()
	return time.Now().Before(c.blockedUntil[host])
}

func (c *client) markBlocked(host string) {
	c.blockMu.Lock()
	defer c.blockMu.Unlock()
	if c.blockedUntil == nil {
		c.blockedUntil = map[string]time.Time{}
	}
	c.blockedUntil[host] = time.Now().Add(cfBlockHold)
}

// http1Transport — HTTP/1.1 без HTTP/2. Отпечаток HTTP/2-клиента Go хорошо
// известен антибот-защитам, а простые клиенты ходят по HTTP/1.1.
func http1Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ForceAttemptHTTP2 = false
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	// Clone переносит в TLS-настройки заявку на h2 (ALPN). Без явного списка
	// сервер выбирает HTTP/2 и шлёт кадр SETTINGS, который HTTP/1.1-клиент
	// принимает за испорченный ответ — так и было в 0.1.1.
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	}
	t.TLSClientConfig.NextProtos = []string{"http/1.1"}
	return t
}

// blockInfo — кто вернул отказ: по заголовкам видно Akamai, Cloudflare или сам Khoros.
func blockInfo(resp *http.Response, body []byte) string {
	var parts []string
	for _, h := range []string{"Server", "Cf-Ray", "X-Akamai-Request-Id", "X-Cache", "Via"} {
		if v := resp.Header.Get(h); v != "" {
			parts = append(parts, h+": "+v)
		}
	}
	b := strings.Join(strings.Fields(stripTags(string(body))), " ")
	if r := []rune(b); len(r) > 200 {
		b = string(r[:200])
	}
	if b != "" {
		parts = append(parts, "body: "+b)
	}
	return strings.Join(parts, "; ")
}

func newClient() *client {
	return &client{
		helpBase:      "https://help.sap.com",
		communityBase: "https://community.sap.com",
		http:          &http.Client{Timeout: requestTimeout, Transport: http1Transport()},
		userAgent:     "sap-help-mcp-go/" + serverVersion + " (+https://github.com/osipovgoga33/sap-help-mcp)",
		cache:         map[string]cacheEntry{},
		gap:           minGap,
	}
}

// buildURL собирает адрес с параметрами; пустые значения отбрасываются.
// Порядок параметров фиксирован (по ключу) — иначе один и тот же запрос
// давал бы разные ключи кэша.
func buildURL(base string, params map[string]string) string {
	q := url.Values{}
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}

func (c *client) cacheGet(u string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.cache[u]; ok && time.Since(e.at) < cacheTTL {
		return e.data
	}
	return nil
}

func (c *client) cachePut(u string, data map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= cacheMax {
		var oldestKey string
		var oldest time.Time
		for k, e := range c.cache {
			if oldestKey == "" || e.at.Before(oldest) {
				oldestKey, oldest = k, e.at
			}
		}
		delete(c.cache, oldestKey)
	}
	c.cache[u] = cacheEntry{at: time.Now(), data: data}
}

func (c *client) cacheSize() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cache)
}

func (c *client) pace() {
	c.gapMu.Lock()
	defer c.gapMu.Unlock()
	if wait := c.gap - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

// getJSON — единственная точка выхода в сеть. Только GET: сервер ничего не
// отправляет порталам, кроме запросов на чтение. Ошибка — понятная строка.
func (c *client) getJSON(u, referer string) (map[string]any, string) {
	if d := c.cacheGet(u); d != nil {
		return d, ""
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, "Bad request URL: " + err.Error()
	}
	if c.hostBlocked(req.URL.Host) {
		return nil, cfMarker + " " + req.URL.Host + " is behind a Cloudflare challenge for this network; not calling it again for a while."
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	c.pace()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "Network unreachable or timed out: " + err.Error()
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, "Failed to read the response: " + err.Error()
	}
	if resp.StatusCode == http.StatusForbidden && isCloudflareChallenge(resp, raw) {
		c.markBlocked(req.URL.Host)
		log.Printf("Cloudflare-проверка на %s (%s), %s не обращаемся", req.URL.Host, resp.Header.Get("Cf-Ray"), cfBlockHold)
		return nil, cfMarker + " " + req.URL.Host + " answered with a Cloudflare challenge (Ray " + resp.Header.Get("Cf-Ray") + ")."
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		log.Printf("отказ %s от %s: %s", resp.Status, req.URL.Host, blockInfo(resp, raw))
		return nil, fmt.Sprintf("SAP responded %s from %s — the portal is throttling or blocking this client. "+
			"Stop calling it for a while; do not retry in a loop. [%s]", resp.Status, req.URL.Host, blockInfo(resp, raw))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Sprintf("SAP responded %s. A 4xx usually means the portal changed its "+
			"request format.", resp.Status)
	}

	var data map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return nil, "Response was not a JSON object — the portal most likely returned an HTML " +
			"page (bot protection, or the endpoint moved)."
	}
	c.cachePut(u, data)
	return data, ""
}

// --- доступ к полям разобранного JSON ---

func getMap(v any, key string) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return nil
	}
	r, _ := m[key].(map[string]any)
	return r
}

func getSlice(v any, key string) []any {
	m, _ := v.(map[string]any)
	if m == nil {
		return nil
	}
	r, _ := m[key].([]any)
	return r
}

func getStr(v any, key string) string {
	m, _ := v.(map[string]any)
	if m == nil {
		return ""
	}
	return toStr(m[key])
}

func toStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

func toInt(v any) int {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i)
		}
		if f, err := x.Float64(); err == nil {
			return int(f)
		}
	case float64:
		return int(x)
	case int:
		return x
	case string:
		i, _ := strconv.Atoi(x)
		return i
	}
	return 0
}

func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true"
	}
	return false
}

// sortedKeys нужен только для стабильного вывода в тестах и статусе.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
