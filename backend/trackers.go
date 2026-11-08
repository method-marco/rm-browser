package main

// Tracking pixels and ad images are skipped before they are downloaded:
// downloading them is what tells the tracker that (and where) you read.
// (Images under 48 px are also thrown away after downloading, see
// images.go; this check saves the request.)

import (
	"net/url"
	"path"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// trackerHosts are ad, analytics and tracking domains (and their
// subdomains). None of them serves pictures that belong to an article.
var trackerHosts = []string{
	// Google
	"doubleclick.net", "googlesyndication.com", "google-analytics.com", "googletagmanager.com",
	"googleadservices.com", "googletagservices.com", "adservice.google.com",
	// social networks' pixels
	"facebook.com", "facebook.net", "ads-twitter.com", "analytics.twitter.com", "t.co",
	"px.ads.linkedin.com", "ct.pinterest.com", "tr.snapchat.com", "analytics.tiktok.com", "q.quora.com",
	"bat.bing.com", "clarity.ms",
	// measurement and analytics
	"scorecardresearch.com", "quantserve.com", "quantcount.com", "chartbeat.com", "chartbeat.net",
	"parsely.com", "omtrdc.net", "demdex.net", "2o7.net", "imrworldwide.com", "hotjar.com",
	"newrelic.com", "nr-data.net", "segment.io", "segment.com", "mixpanel.com", "amplitude.com",
	"permutive.com", "cxense.com", "pixel.wp.com", "stats.wp.com", "mc.yandex.ru", "statcounter.com",
	"plausible.io", "matomo.cloud", "piwik.pro", "heap.io", "fullstory.com",
	// ad exchanges, data brokers, "around the web" boxes
	"adnxs.com", "criteo.com", "criteo.net", "taboola.com", "outbrain.com", "amazon-adsystem.com",
	"krxd.net", "bluekai.com", "rlcdn.com", "adsrvr.org", "pubmatic.com", "rubiconproject.com",
	"openx.net", "casalemedia.com", "moatads.com", "doubleverify.com", "adsafeprotected.com",
	"mathtag.com", "tapad.com", "agkn.com", "mookie1.com", "sharethrough.com", "yieldmo.com",
	"lijit.com", "sitescout.com", "everesttech.net", "bidswitch.net", "smartadserver.com",
	"teads.tv", "media.net", "zemanta.com", "revcontent.com", "mgid.com", "33across.com",
	"indexww.com", "contextweb.com", "turn.com", "exelator.com", "eyeota.net", "liadm.com",
	"id5-sync.com", "crwdcntrl.net", "adform.net", "serving-sys.com", "flashtalking.com",
}

// trackerLabels: hosts whose first label says what they are
// (pixel.example.com, beacon.example.com).
var trackerLabels = map[string]bool{
	"pixel": true, "pixels": true, "beacon": true, "beacons": true, "tracking": true, "track": true,
	"tracker": true, "analytics": true, "metrics": true, "telemetry": true, "collector": true,
	"ad": true, "ads": true, "adserver": true, "adsrv": true,
}

// trackerFiles: typical names of invisible images.
var trackerFiles = map[string]bool{
	"pixel.gif": true, "pixel.png": true, "pixel.jpg": true, "spacer.gif": true, "blank.gif": true,
	"clear.gif": true, "transparent.gif": true, "transparent.png": true, "1x1.gif": true,
	"1x1.png": true, "trans.gif": true, "dot.gif": true, "b.gif": true, "p.gif": true, "t.gif": true,
}

// isTrackerImage reports whether an <img> (n, may be nil) with address src
// is a tracking pixel or an ad, judged without downloading it.
func isTrackerImage(n *html.Node, src string) bool {
	if n != nil {
		// declared tiny: width="1" height="1" and the like
		for _, a := range []string{"width", "height"} {
			if v, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(attr(n, a)), "px")); err == nil && v >= 0 && v < minImageSide {
				return true
			}
		}
	}
	u, err := url.Parse(src)
	if err != nil {
		return true
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	for _, t := range trackerHosts {
		if host == t || strings.HasSuffix(host, "."+t) {
			return true
		}
	}
	if i := strings.IndexByte(host, '.'); i > 0 && trackerLabels[host[:i]] {
		return true
	}
	return trackerFiles[strings.ToLower(path.Base(u.Path))]
}
