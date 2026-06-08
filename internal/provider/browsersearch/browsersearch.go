package browsersearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"

	"pacp/internal/contracts"
	"pacp/internal/provider"
)

const (
	SearchCapabilityID = "cap_search_query"
	FetchCapabilityID  = "cap_browser_fetch"

	defaultServiceID   = "svc_browser_search"
	defaultServiceName = "Browser Search Provider"
	defaultVersion     = "0.1.0"
	defaultMaxBytes    = 1 << 20
	maxAllowedBytes    = 5 << 20
)

type Config struct {
	Endpoint        string
	ServiceID       string
	ServiceName     string
	Version         string
	AuthCredential  string
	SearchIndexPath string
	AllowedHosts    []string
	AllowHTTP       bool
	Timeout         time.Duration
	Client          *http.Client
}

type SearchItem struct {
	Title    string         `json:"title"`
	URL      string         `json:"url"`
	Snippet  string         `json:"snippet,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type indexFile struct {
	Items []SearchItem `json:"items"`
}

type browserSearchProvider struct {
	index        []SearchItem
	client       *http.Client
	allowedHosts []string
	allowHTTP    bool
	timeout      time.Duration
}

type searchResult struct {
	item  SearchItem
	score int
}

type searchSafety struct {
	AllowedHosts     []string `json:"allowed_hosts"`
	AllowHTTPResults bool     `json:"allow_http_results"`
	Scoped           bool     `json:"-"`
}

func NewServer(cfg Config) (*provider.Server, error) {
	normalized := normalizeConfig(cfg)
	index, err := loadSearchIndex(normalized.SearchIndexPath)
	if err != nil {
		return nil, err
	}
	p := &browserSearchProvider{
		index:        index,
		allowedHosts: normalized.AllowedHosts,
		allowHTTP:    normalized.AllowHTTP,
		timeout:      normalized.Timeout,
	}
	p.client = normalized.Client
	if p.client == nil {
		p.client = &http.Client{
			Timeout: normalized.Timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return p.validateURL(req.URL.String())
			},
		}
	}
	return provider.NewServerWithOptions(manifest(normalized), map[string]provider.CapabilityHandler{
		SearchCapabilityID: p.search,
		FetchCapabilityID:  p.fetch,
	}, provider.WithAuthCredential(normalized.AuthCredential))
}

func normalizeConfig(cfg Config) Config {
	if cfg.ServiceID == "" {
		cfg.ServiceID = defaultServiceID
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = defaultServiceName
	}
	if cfg.Version == "" {
		cfg.Version = defaultVersion
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if len(cfg.AllowedHosts) == 0 {
		cfg.AllowedHosts = []string{"localhost", "127.0.0.1", "::1"}
	}
	for i, host := range cfg.AllowedHosts {
		cfg.AllowedHosts[i] = strings.ToLower(strings.TrimSpace(host))
	}
	return cfg
}

func loadSearchIndex(path string) ([]SearchItem, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var object indexFile
	if err := json.Unmarshal(body, &object); err == nil && object.Items != nil {
		return object.Items, nil
	}
	var array []SearchItem
	if err := json.Unmarshal(body, &array); err != nil {
		return nil, fmt.Errorf("decode search index %s: %w", path, err)
	}
	return array, nil
}

func (p *browserSearchProvider) search(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	query, err := requiredString(req.Input, "query")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: query must contain at least one search term", provider.ErrValidation)
	}
	safety, err := parseSearchSafety(req.Input)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	limit := intInput(req.Input, "limit", 10, 50)
	results := make([]searchResult, 0, len(p.index))
	filteredCount := 0
	for _, item := range p.index {
		if !safety.allows(item.URL) {
			filteredCount++
			continue
		}
		score := scoreItem(item, terms)
		if score > 0 {
			results = append(results, searchResult{item: item, score: score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score == results[j].score {
			return results[i].item.Title < results[j].item.Title
		}
		return results[i].score > results[j].score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	items := make([]any, 0, len(results))
	for _, result := range results {
		items = append(items, map[string]any{
			"title":    result.item.Title,
			"url":      result.item.URL,
			"snippet":  result.item.Snippet,
			"tags":     stringsSliceAsAny(result.item.Tags),
			"metadata": result.item.Metadata,
			"score":    result.score,
		})
	}
	return contracts.ProviderInvokeResponse{Output: map[string]any{
		"query": query,
		"count": len(items),
		"items": items,
		"safety": map[string]any{
			"allowed_hosts":      stringsSliceAsAny(safety.AllowedHosts),
			"allow_http_results": safety.AllowHTTPResults,
			"filtered_count":     filteredCount,
		},
	}}, nil
}

func (p *browserSearchProvider) fetch(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	rawURL, err := requiredString(req.Input, "url")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	action, err := browserAction(req.Input)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	if err := p.validateURL(rawURL); err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	maxBytes := intInput(req.Input, "max_bytes", defaultMaxBytes, maxAllowedBytes)
	extractText := boolInput(req.Input, "extract_text", true)
	includeLinks := boolInput(req.Input, "include_links", true)
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	httpReq.Header.Set("Accept", "text/html,text/plain;q=0.9,*/*;q=0.1")
	httpReq.Header.Set("User-Agent", "pacp-browser-search-provider/0.1")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: %s", provider.ErrBackend, err)
	}
	defer resp.Body.Close()
	if err := p.validateURL(resp.Request.URL.String()); err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	body, truncated, err := readLimited(resp.Body, maxBytes)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	contentType := resp.Header.Get("Content-Type")
	baseURL := resp.Request.URL
	title := ""
	text := ""
	links := []any{}
	normalizedType := strings.ToLower(strings.Split(contentType, ";")[0])
	switch {
	case strings.Contains(normalizedType, "html"):
		extracted, err := extractHTML(body, baseURL, includeLinks)
		if err != nil {
			return contracts.ProviderInvokeResponse{}, err
		}
		title = extracted.title
		if extractText {
			text = extracted.text
		}
		links = extracted.links
	case strings.HasPrefix(normalizedType, "text/") && extractText:
		text = normalizeWhitespace(string(body))
	}
	return contracts.ProviderInvokeResponse{Output: map[string]any{
		"action":       action,
		"url":          baseURL.String(),
		"status":       resp.StatusCode,
		"content_type": contentType,
		"title":        title,
		"text":         text,
		"links":        links,
		"truncated":    truncated,
	}}, nil
}

func (p *browserSearchProvider) validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: url is invalid", provider.ErrValidation)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("%w: url must use http or https", provider.ErrValidation)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("%w: url host is required", provider.ErrValidation)
	}
	if parsed.Scheme == "http" && !p.allowHTTP && !isLoopbackHost(host) {
		return fmt.Errorf("%w: http urls are allowed only for loopback hosts unless allow_http is enabled", provider.ErrValidation)
	}
	if !p.hostAllowed(host) {
		return fmt.Errorf("%w: host %s is not allowed", provider.ErrValidation, host)
	}
	return nil
}

func (p *browserSearchProvider) hostAllowed(host string) bool {
	return hostAllowed(host, p.allowedHosts)
}

func hostAllowed(host string, patterns []string) bool {
	for _, pattern := range patterns {
		switch {
		case pattern == "*":
			return true
		case strings.HasPrefix(pattern, "*."):
			suffix := strings.TrimPrefix(pattern, "*.")
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return true
			}
		case host == pattern:
			return true
		}
	}
	return false
}

func (s searchSafety) allows(rawURL string) bool {
	if !s.Scoped {
		return true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	if len(s.AllowedHosts) > 0 && !hostAllowed(host, s.AllowedHosts) {
		return false
	}
	if scheme == "http" && !s.AllowHTTPResults && !isLoopbackHost(host) {
		return false
	}
	return true
}

func scoreItem(item SearchItem, terms []string) int {
	text := strings.ToLower(strings.Join([]string{item.Title, item.Snippet, item.URL, strings.Join(item.Tags, " ")}, " "))
	score := 0
	for _, term := range terms {
		if strings.Contains(strings.ToLower(item.Title), term) {
			score += 5
		}
		if strings.Contains(strings.ToLower(item.Snippet), term) {
			score += 3
		}
		if strings.Contains(strings.ToLower(item.URL), term) {
			score += 2
		}
		if strings.Contains(text, term) {
			score++
		}
	}
	return score
}

func readLimited(body io.Reader, maxBytes int) ([]byte, bool, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	limited, err := io.ReadAll(io.LimitReader(body, int64(maxBytes)+1))
	if err != nil {
		return nil, false, err
	}
	if len(limited) > maxBytes {
		return limited[:maxBytes], true, nil
	}
	return limited, false, nil
}

type htmlExtraction struct {
	title string
	text  string
	links []any
}

func extractHTML(body []byte, baseURL *url.URL, includeLinks bool) (htmlExtraction, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return htmlExtraction{}, err
	}
	out := htmlExtraction{links: []any{}}
	textParts := []string{}
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, skip bool) {
		if node.Type == html.ElementNode {
			tag := strings.ToLower(node.Data)
			if tag == "script" || tag == "style" || tag == "noscript" {
				skip = true
			}
			if tag == "title" && out.title == "" {
				out.title = normalizeWhitespace(nodeText(node))
			}
			if tag == "a" && includeLinks && len(out.links) < 100 {
				if href := attr(node, "href"); href != "" {
					if resolved := resolveURL(baseURL, href); resolved != "" {
						out.links = append(out.links, map[string]any{
							"url":  resolved,
							"text": normalizeWhitespace(nodeText(node)),
						})
					}
				}
			}
		}
		if node.Type == html.TextNode && !skip {
			if text := normalizeWhitespace(node.Data); text != "" {
				textParts = append(textParts, text)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, skip)
		}
	}
	walk(doc, false)
	out.text = truncateText(normalizeWhitespace(strings.Join(textParts, " ")), 20000)
	return out, nil
}

func nodeText(node *html.Node) string {
	parts := []string{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			parts = append(parts, n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(parts, " ")
}

func attr(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func resolveURL(base *url.URL, raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if base != nil {
		parsed = base.ResolveReference(parsed)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return parsed.String()
}

func normalizeWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func truncateText(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return text[:max]
}

func requiredString(input map[string]any, key string) (string, error) {
	value, _ := input[key].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", provider.ErrValidation, key)
	}
	return value, nil
}

func intInput(input map[string]any, key string, fallback, max int) int {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback
	}
	out := fallback
	switch typed := value.(type) {
	case int:
		out = typed
	case int64:
		out = int(typed)
	case float64:
		out = int(typed)
	}
	if out <= 0 {
		out = fallback
	}
	if max > 0 && out > max {
		out = max
	}
	return out
}

func boolInput(input map[string]any, key string, fallback bool) bool {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback
	}
	typed, ok := value.(bool)
	if !ok {
		return fallback
	}
	return typed
}

func parseSearchSafety(input map[string]any) (searchSafety, error) {
	allowedHosts, hasAllowedHosts, err := optionalStringSlice(input, "allowed_hosts")
	if err != nil {
		return searchSafety{}, err
	}
	_, hasHTTPPolicy := input["allow_http_results"]
	return searchSafety{
		AllowedHosts:     allowedHosts,
		AllowHTTPResults: boolInput(input, "allow_http_results", false),
		Scoped:           hasAllowedHosts || hasHTTPPolicy,
	}, nil
}

func optionalStringSlice(input map[string]any, key string) ([]string, bool, error) {
	raw, ok := input[key]
	if !ok || raw == nil {
		return nil, false, nil
	}
	values := []string{}
	switch typed := raw.(type) {
	case []string:
		values = append(values, typed...)
	case []any:
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				return nil, true, fmt.Errorf("%w: %s must contain only strings", provider.ErrValidation, key)
			}
			values = append(values, value)
		}
	default:
		return nil, true, fmt.Errorf("%w: %s must be an array of strings", provider.ErrValidation, key)
	}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return nil, true, fmt.Errorf("%w: %s must not contain empty values", provider.ErrValidation, key)
		}
		normalized = append(normalized, value)
	}
	return normalized, true, nil
}

func browserAction(input map[string]any) (string, error) {
	value, _ := input["action"].(string)
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "fetch", nil
	}
	switch value {
	case "fetch", "extract":
		return value, nil
	default:
		return "", fmt.Errorf("%w: action %s is not supported", provider.ErrValidation, value)
	}
}

func stringsSliceAsAny(values []string) []any {
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	return items
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func manifest(cfg Config) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           cfg.ServiceID,
			Name:         cfg.ServiceName,
			Description:  "Constrained search and guarded browser fetch provider.",
			Version:      cfg.Version,
			ProviderKind: "browser_search",
			Tags:         []string{"browser", "search"},
		},
		Provider: contracts.Provider{Endpoint: cfg.Endpoint, HealthPath: "/v1/provider/health"},
		Capabilities: []contracts.Capability{
			{
				ID:            SearchCapabilityID,
				Name:          "Search query",
				Description:   "Search a configured index and return structured result metadata.",
				Tags:          []string{"search"},
				ExecutionMode: "sync",
				InputSchema: map[string]any{
					"type":     "object",
					"required": []any{"query"},
					"properties": map[string]any{
						"query":              map[string]any{"type": "string"},
						"limit":              map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
						"allowed_hosts":      map[string]any{"type": "array"},
						"allow_http_results": map[string]any{"type": "boolean"},
					},
				},
				OutputSchema: map[string]any{
					"type":     "object",
					"required": []any{"query", "count", "items", "safety"},
					"properties": map[string]any{
						"query":  map[string]any{"type": "string"},
						"count":  map[string]any{"type": "integer"},
						"items":  map[string]any{"type": "array"},
						"safety": map[string]any{"type": "object"},
					},
				},
				Examples:      []map[string]any{{"query": "artifact upload", "limit": 5, "allowed_hosts": []string{"docs.local"}}},
				SideEffects:   "none",
				ResourceHints: []contracts.ResourceHint{},
				ArtifactHints: []contracts.ArtifactHint{},
				TimeoutHint:   "10s",
			},
			{
				ID:            FetchCapabilityID,
				Name:          "Browser fetch",
				Description:   "Fetch an allowed HTTP(S) page and extract title, text, and links.",
				Tags:          []string{"browser", "fetch", "extract"},
				ExecutionMode: "sync",
				InputSchema: map[string]any{
					"type":     "object",
					"required": []any{"url"},
					"properties": map[string]any{
						"action":       map[string]any{"type": "string", "enum": []any{"fetch", "extract"}},
						"url":          map[string]any{"type": "string"},
						"extract_text": map[string]any{"type": "boolean"},
						"include_links": map[string]any{
							"type": "boolean",
						},
						"max_bytes": map[string]any{"type": "integer"},
					},
				},
				OutputSchema: map[string]any{
					"type":     "object",
					"required": []any{"action", "url", "status", "content_type", "truncated"},
					"properties": map[string]any{
						"action":       map[string]any{"type": "string"},
						"url":          map[string]any{"type": "string"},
						"status":       map[string]any{"type": "integer"},
						"content_type": map[string]any{"type": "string"},
						"title":        map[string]any{"type": "string"},
						"text":         map[string]any{"type": "string"},
						"links":        map[string]any{"type": "array"},
						"truncated":    map[string]any{"type": "boolean"},
					},
				},
				Examples:      []map[string]any{{"action": "fetch", "url": "https://example.com", "extract_text": true, "include_links": true}},
				SideEffects:   "external",
				ResourceHints: []contracts.ResourceHint{},
				ArtifactHints: []contracts.ArtifactHint{},
				TimeoutHint:   "30s",
			},
		},
	}
}
