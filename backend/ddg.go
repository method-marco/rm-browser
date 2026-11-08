package main

// DuckDuckGo search through its JavaScript-free HTML endpoint
// (html.duckduckgo.com/html/) and its autocomplete endpoint (duckduckgo.com/ac/).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

var (
	ddgSearchURL  = "https://html.duckduckgo.com/html/"
	ddgSuggestURL = "https://duckduckgo.com/ac/"
)

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Host    string `json:"host"`
	Snippet string `json:"snippet"`
}

type SearchPage struct {
	Query   string            `json:"query"`
	Results []SearchResult    `json:"results"`
	Next    map[string]string `json:"next,omitempty"` // form values for the next result page
}

// safe search: "moderate" (default), "strict" or "off"
func safeSearchValue(s string) string {
	switch s {
	case "strict":
		return "1"
	case "off":
		return "-2"
	}
	return "-1"
}

func ddgSearch(ctx context.Context, c *http.Client, query string, next map[string]string, safe string) (*SearchPage, error) {
	form := url.Values{}
	for k, v := range next {
		form.Set(k, v)
	}
	form.Set("q", query)
	if form.Get("kl") == "" {
		form.Set("kl", "wt-wt")
	}
	form.Set("kp", safeSearchValue(safe))
	req, err := newRequest(ctx, http.MethodPost, ddgSearchURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "https://html.duckduckgo.com/")
	f, err := fetch(ctx, c, req, maxPageBytes)
	if err != nil {
		return nil, err
	}
	page, err := parseDDG(string(f.Body))
	if err != nil {
		return nil, err
	}
	page.Query = query
	return page, nil
}

func parseDDG(src string) (*SearchPage, error) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return nil, err
	}
	page := &SearchPage{Results: []SearchResult{}}
	captcha := false
	walk(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		cls := " " + attr(n, "class") + " "
		switch {
		case strings.Contains(cls, " anomaly-modal") || strings.Contains(cls, " challenge-form"):
			captcha = true
		case strings.Contains(cls, " result ") || strings.HasPrefix(cls, " result "):
			if strings.Contains(cls, "result--ad") {
				return false
			}
			if r, ok := parseDDGResult(n); ok {
				page.Results = append(page.Results, r)
			}
			return false
		case strings.Contains(cls, " nav-link "):
			if f := findFirst(n, func(m *html.Node) bool { return m.Type == html.ElementNode && m.Data == "form" }); f != nil {
				vals := map[string]string{}
				isNext := false
				walk(f, func(m *html.Node) bool {
					if m.Type == html.ElementNode && m.Data == "input" {
						if attr(m, "type") == "submit" && strings.EqualFold(attr(m, "value"), "next") {
							isNext = true
						}
						if name := attr(m, "name"); name != "" {
							vals[name] = attr(m, "value")
						}
					}
					return true
				})
				if isNext {
					delete(vals, "q")
					page.Next = vals
				}
			}
			return false
		}
		return true
	})
	if captcha && len(page.Results) == 0 {
		return nil, errors.New("DuckDuckGo wants to check that you are not a bot. Please try again in a few minutes")
	}
	return page, nil
}

func parseDDGResult(n *html.Node) (SearchResult, bool) {
	var r SearchResult
	walk(n, func(m *html.Node) bool {
		if m.Type != html.ElementNode {
			return true
		}
		cls := " " + attr(m, "class") + " "
		switch {
		case strings.Contains(cls, " result__a "):
			r.Title = collapseSpace(textOf(m))
			r.URL = ddgTarget(attr(m, "href"))
			return false
		case strings.Contains(cls, " result__snippet "):
			r.Snippet = collapseSpace(textOf(m))
			return false
		}
		return true
	})
	if r.Title == "" || r.URL == "" {
		return r, false
	}
	if u, err := url.Parse(r.URL); err == nil {
		r.Host = strings.TrimPrefix(u.Hostname(), "www.")
	}
	return r, true
}

// ddgTarget turns DuckDuckGo's redirect links (//duckduckgo.com/l/?uddg=...)
// into the real address, so DuckDuckGo isn't asked again for every click.
func ddgTarget(href string) string {
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if strings.HasSuffix(u.Hostname(), "duckduckgo.com") && u.Path == "/l/" {
		if t := u.Query().Get("uddg"); t != "" {
			href = t
			u, err = url.Parse(t)
			if err != nil {
				return ""
			}
		}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	if strings.HasSuffix(u.Hostname(), "duckduckgo.com") && strings.HasPrefix(u.Path, "/y.js") {
		return "" // ad click link
	}
	return href
}

func ddgSuggest(ctx context.Context, c *http.Client, query string) ([]string, error) {
	u := ddgSuggestURL + "?" + url.Values{"q": {query}, "kl": {"wt-wt"}}.Encode()
	req, err := newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	f, err := fetch(ctx, c, req, 1<<20)
	if err != nil {
		return nil, err
	}
	return parseSuggestions(f.Body)
}

func parseSuggestions(b []byte) ([]string, error) {
	var items []struct {
		Phrase string `json:"phrase"`
	}
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	out := []string{}
	for _, it := range items {
		if it.Phrase != "" {
			out = append(out, it.Phrase)
		}
	}
	return out, nil
}

// ---- small HTML helpers

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// walk visits n and its descendants depth first; fn returns false to skip
// the children of a node.
func walk(n *html.Node, fn func(*html.Node) bool) {
	if !fn(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}

func findFirst(n *html.Node, pred func(*html.Node) bool) *html.Node {
	var found *html.Node
	walk(n, func(m *html.Node) bool {
		if found != nil {
			return false
		}
		if pred(m) {
			found = m
			return false
		}
		return true
	})
	return found
}

func textOf(n *html.Node) string {
	var b strings.Builder
	walk(n, func(m *html.Node) bool {
		if m.Type == html.TextNode {
			b.WriteString(m.Data)
		}
		return !(m.Type == html.ElementNode && (m.Data == "script" || m.Data == "style"))
	})
	return b.String()
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }
