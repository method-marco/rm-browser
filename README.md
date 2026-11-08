# rm-browser

An [AppLoad](https://github.com/asivery/rm-appload) app for reMarkable tablets: a
**distraction-free web browser** that shows every page in **reader view**, split into
**pages you turn** like a book, with **DuckDuckGo** search and autocomplete.

> **Status: early / experimental.** Not affiliated with DuckDuckGo.

## Features

- **Reader view only:** articles are extracted with Mozilla's Readability (the engine behind
  Firefox's Reader View, via [go-readability](https://codeberg.org/readeck/go-readability)).
  Ads, menus, pop-ups, cookie banners and scripts are gone. Pages without an article (a site's front
  page) are shown as a simplified page: the story list with headlines, teasers and pictures
- **Pages, not scrolling:** the text is split into screen pages, and no line or picture is ever cut
  at the bottom edge. Tap the **right** side (or swipe left) for the next page, the **left** side to go
  back, the **middle** for the menu. Page slider in the menu. Your page is remembered in the history
- **Contents** (like a book's table of contents): the article's headings with their page numbers
  (tap to jump), and the site's navigation menu as a list of links
- **DuckDuckGo** search with **autocomplete** while you type, plus matching pages and searches
  from your history. Results are shown page by page, *More results* loads the next batch.
  Type an address (`road.cc`, `https://…`) to open it directly
- **One tab.** *Back*, *Forward* and *Home* in the menu
- **Clear browsing data:** one button (home screen, menu, Settings) deletes history and searches,
  the back/forward list, cookies, and cached pages and images. Settings stay
- Settings: text size, font (Noto Sans or EB Garamond, the tablet's own fonts), images on/off,
  safe search
- Images are converted to grey scale and sized to fit the screen; e-ink-friendly UI (black/white,
  no animations, large touch targets); always full screen

## Security

Security comes from established libraries, not home-made code, and from never running what a
website sends:

- **No JavaScript, ever.** Pages are only parsed, never executed. (A Chromium engine isn't
  available to AppLoad apps; it would also be the opposite of distraction-free.)
- **HTTPS** with Go's TLS stack: certificates verified (system roots, or Mozilla's root store built
  into the app via `golang.org/x/crypto/x509roots/fallback`), TLS 1.2+ only. Invalid certificates
  are refused. Plain `http://` pages are marked *not secure*
- HTML is cleaned by Readability and then by [bluemonday](https://github.com/microcosm-cc/bluemonday)
  (strict allowlist; links only `http`/`https`). The app then writes its own, minimal rich text
  from that, with all page text escaped
- **Cookies live in memory only**, never on the tablet; *Clear browsing data* throws them away

### Sandbox

Everything that touches the network or website data runs in the backend process, which locks
itself in before loading anything. AppLoad starts it as root, then:

1. **Drops root:** it switches to the unprivileged user `nobody` (uid 65534) without groups. It
   keeps only handles to its own two folders (`os.Root`: no escaping them via `..` or symlinks).
   Your notebooks, xochitl and the system are out of reach
2. **no_new_privs:** it can never gain privileges again
3. **[Landlock](https://landlock.io)** (via [go-landlock](https://github.com/landlock-lsm/go-landlock),
   if the tablet's kernel has it): files only in `/etc` (read-only: DNS, certificates) and its two
   folders; network only outgoing to web ports (80, 443, 8080, 8443) and DNS, no listening; no
   signals to processes outside the sandbox
4. **seccomp** (via [go-seccomp-bpf](https://github.com/elastic/go-seccomp-bpf)): system calls a
   browser never needs fail: starting programs (`execve`), debugging other processes, mounting,
   kernel modules, BPF, namespaces, changing user ids, setting the clock, …

Images are decoded inside the sandbox by Go's memory-safe decoders and re-encoded as new JPEG
files, so the frontend (in xochitl, which runs as root) never opens anything a website sent.
If the sandbox can't be set up, the browser refuses to run. *Settings* shows the active layers.

## Limits

- No JavaScript means pages that build their content with scripts stay empty (the app says so),
  and there are no logins, comments or forms
- DuckDuckGo sometimes asks to prove you're not a bot (more likely after many quick searches).
  The app then asks you to try again in a few minutes
- Sites on unusual ports (other than 80, 443, 8080, 8443) can't be opened

## Requirements

- reMarkable 1, 2, Paper Pro or Paper Pro Move with [xovi](https://github.com/asivery/xovi) +
  [AppLoad](https://github.com/asivery/rm-appload)
- Wi-Fi on the tablet

## Install

Download `rm-browser-aarch64.tar.gz` (Paper Pro / Move) or `rm-browser-armv7.tar.gz`
(reMarkable 1/2) from the [releases](../../releases), or build it (see below), and extract it on
the device:

```sh
scp rm-browser-aarch64.tar.gz root@10.11.99.1:
ssh root@10.11.99.1 'tar -C /home/root/xovi/exthome/appload -xzf rm-browser-aarch64.tar.gz'
```

Or in one step from a checkout: `make install ARCH=aarch64 HOST=10.11.99.1`.

Then reload AppLoad. The app shows up as **Browser**.

## Files on the device

| Path | Contents |
|---|---|
| `/home/root/.config/rm-browser/config.json` | settings, history, back/forward list |
| `/home/root/.cache/rm-browser/` | converted images |

Both folders belong to user `nobody` (the sandbox). Cookies are never written to disk.

## Building

Needs Go ≥ 1.26 and Qt 6's `rcc`, or just Docker:

```sh
make docker            # builds dist/*.tar.gz and runs the tests in a container
make test              # backend unit tests, incl. a test that the sandbox really blocks things
make qmltest           # frontend tests: headless Qt in Docker with simulated taps and swipes
make install ARCH=aarch64 HOST=10.11.99.1   # build and copy to a device over SSH

# check against real sites (DuckDuckGo, Wikipedia, macrumors, engadget, cyclingweekly, road.cc):
cd backend && RM_BROWSER_LIVE=1 go test -run 'Live|Sites' -v ./...
# ... and also write them as fixtures for `make qmltest` (paginates them, checks no line is cut):
RM_BROWSER_LIVE=1 RM_BROWSER_FIXTURE=$PWD/../build/fixture go test -run Sites ./... && (cd ../build/fixture && ls *.json > index.txt)
```

## Layout

```
app/
  manifest.json        AppLoad manifest (id "rm-browser", loadsBackend)
  ui/main.qml          frontend: reader with pagination, search results, address bar, contents
  icon.png             generated by scripts/make_icon.py
backend/               Go backend -> backend/entry (static, no cgo)
  appload.go           AppLoad socket protocol
  sandbox_linux.go     privilege drop, Landlock, seccomp
  store.go             config and cache folders (os.Root)
  fetch.go             HTTP client, address-or-search detection
  ddg.go               DuckDuckGo search and autocomplete
  reader.go            Readability + bluemonday + Qt rich text writer
  images.go            image download, grey scale, resizing
  main.go              messages, history, back/forward, clearing data
tests/qml/             headless frontend tests (stub AppLoad module, Dockerfile)
packaging/vellum/browser/VELBUILD
```

## License

MIT
