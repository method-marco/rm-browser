package main

// Videos can't play in the reader (no JavaScript, no video on e-ink), and
// the cleaner drops players and embeds. So that nothing silently goes
// missing, each video becomes a line "Video: <title>" linking to it (in a
// saved PDF that link opens on any other device). This runs on the raw
// page, before Readability, so the line stays where the video was.

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// videoHosts are players whose embeds are videos (and not ads or forms).
var videoHosts = []string{
	"youtube.com", "youtube-nocookie.com", "youtu.be", "vimeo.com", "dailymotion.com", "dai.ly",
	"twitch.tv", "tiktok.com", "streamable.com", "jwplayer.com", "jwplatform.com", "brightcove.net",
	"brightcove.com", "wistia.com", "wistia.net", "vidyard.com", "loom.com", "rumble.com", "ted.com",
	"facebook.com/plugins/video", "instagram.com/reel", "bilibili.com", "nicovideo.jp", "peertube",
	"archive.org/embed", "player.", "video.",
}

func isVideoHost(u *url.URL) bool {
	hp := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www.") + u.Path)
	for _, h := range videoHosts {
		if strings.HasPrefix(hp, h) || strings.Contains(hp, "."+h) {
			return true
		}
	}
	return false
}

// videoLink turns a player address into the page to watch the video on.
func videoLink(u *url.URL) string {
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case (host == "youtube.com" || host == "youtube-nocookie.com" || host == "m.youtube.com") && len(parts) >= 2 && (parts[0] == "embed" || parts[0] == "v") && parts[1] != "videoseries":
		return "https://www.youtube.com/watch?v=" + url.QueryEscape(parts[1])
	case host == "youtube.com" && len(parts) >= 1 && parts[0] == "embed" && u.Query().Get("list") != "":
		return "https://www.youtube.com/playlist?list=" + url.QueryEscape(u.Query().Get("list"))
	case host == "player.vimeo.com" && len(parts) >= 2 && parts[0] == "video":
		return "https://vimeo.com/" + url.PathEscape(parts[1])
	case host == "dailymotion.com" && len(parts) >= 3 && parts[0] == "embed" && parts[1] == "video":
		return "https://www.dailymotion.com/video/" + url.PathEscape(parts[2])
	}
	v := *u
	v.Fragment = ""
	return v.String()
}

func videoName(u *url.URL) string {
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	for _, k := range []struct{ host, name string }{
		{"youtu", "YouTube"}, {"vimeo", "Vimeo"}, {"dailymotion", "Dailymotion"}, {"twitch", "Twitch"},
		{"tiktok", "TikTok"}, {"facebook", "Facebook"}, {"instagram", "Instagram"},
	} {
		if strings.Contains(host, k.host) {
			return k.name + " video"
		}
	}
	return "Video on " + host
}

// markVideos replaces the videos in doc with links to them.
func markVideos(doc *html.Node, base *url.URL) {
	var found []*html.Node
	walk(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		switch n.Data {
		case "iframe", "embed", "object", "video", "lite-youtube", "lite-vimeo", "amp-youtube", "amp-vimeo", "amp-video", "amp-iframe":
			found = append(found, n)
			return false
		}
		return true
	})
	last := ""
	for _, n := range found {
		link, title := videoSource(n, base)
		if link == "" || n.Parent == nil {
			continue
		}
		if link == last { // the same video twice in a row (player + fallback)
			n.Parent.RemoveChild(n)
			continue
		}
		last = link
		p := &html.Node{Type: html.ElementNode, Data: "p", DataAtom: atom.P}
		lead := &html.Node{Type: html.ElementNode, Data: "i", DataAtom: atom.I}
		lead.AppendChild(&html.Node{Type: html.TextNode, Data: "Video: "})
		a := &html.Node{Type: html.ElementNode, Data: "a", DataAtom: atom.A, Attr: []html.Attribute{{Key: "href", Val: link}}}
		a.AppendChild(&html.Node{Type: html.TextNode, Data: title})
		p.AppendChild(lead)
		p.AppendChild(a)
		n.Parent.InsertBefore(p, n)
		n.Parent.RemoveChild(n)
	}
}

// videoSource finds where a video element's video can be watched, and a
// name for it; "" if it isn't a video.
func videoSource(n *html.Node, base *url.URL) (link, title string) {
	resolve := func(s string) *url.URL {
		s = strings.TrimSpace(s)
		if s == "" || strings.HasPrefix(s, "data:") || strings.HasPrefix(s, "blob:") {
			return nil
		}
		u, err := base.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil
		}
		return u
	}
	title = strings.TrimSpace(firstNonEmpty(attr(n, "title"), attr(n, "aria-label"), attr(n, "playlabel"), attr(n, "data-title")))
	var u *url.URL
	switch n.Data {
	case "lite-youtube", "amp-youtube":
		if id := firstNonEmpty(attr(n, "videoid"), attr(n, "data-videoid")); id != "" {
			u, _ = url.Parse("https://www.youtube.com/embed/" + url.PathEscape(id))
		}
	case "lite-vimeo", "amp-vimeo":
		if id := firstNonEmpty(attr(n, "videoid"), attr(n, "data-videoid")); id != "" {
			u, _ = url.Parse("https://player.vimeo.com/video/" + url.PathEscape(id))
		}
	case "video", "amp-video":
		for _, k := range []string{"src", "data-src"} {
			if u = resolve(attr(n, k)); u != nil {
				break
			}
		}
		for c := n.FirstChild; c != nil && u == nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "source" {
				u = resolve(firstNonEmpty(attr(c, "src"), attr(c, "data-src")))
			}
		}
		if u == nil {
			return "", ""
		}
		if title == "" {
			title = lastPathPart(u) // file name, or the host
		}
		return u.String(), title
	default: // iframe, embed, object
		u = resolve(firstNonEmpty(attr(n, "src"), attr(n, "data-src"), attr(n, "data-lazy-src"), attr(n, "data")))
		if u == nil || !isVideoHost(u) {
			return "", ""
		}
	}
	if u == nil {
		return "", ""
	}
	if title == "" || strings.EqualFold(title, "youtube video player") || strings.EqualFold(title, "video player") {
		title = videoName(u)
	}
	return videoLink(u), title
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
