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
  colour or grey,
  safe search
- Images in colour (for the Paper Pro's colour screen) or grey scale (Settings), sized to fit
  the screen; e-ink-friendly UI (black/white,
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

1. **Its own folders only:** it hands its two data folders to the user `nobody` and keeps handles
   to them (`os.Root`: nothing escapes them via `..`, absolute paths or symlinks)
2. **chroot jail:** it locks itself into an empty, root-owned folder
   (`/home/root/.cache/rm-browser-jail`) holding only copies of `resolv.conf` and `hosts` for DNS.
   The rest of the tablet's files, your notebooks and `/proc` no longer exist for it
3. **Drops root:** user and group `nobody` (65534), no groups, no capabilities, not dumpable (no
   core dumps, other processes can't read its memory), limits on file size and open files
4. **seccomp allowlist** with `no_new_privs`: on the Paper Pro / Move (arm64) only the ~70
   system calls the backend actually uses reach the kernel; every other call fails with
   `ENOSYS` before any kernel code behind it runs, so kernel bugs there can't be used. The list
   comes from Go's runtime plus a trace of the backend in many situations
   (`scripts/trace-syscalls.sh`). On top: sockets only IPv4/IPv6 (no Unix sockets to D-Bus or
   systemd, no netlink), signals only to itself, and a wrong CPU architecture kills the process.
   On reMarkable 1/2 (32-bit, not traced) a list of dangerous calls is blocked instead
5. **[Landlock](https://landlock.io)** (via [go-landlock](https://github.com/landlock-lsm/go-landlock))
   on kernels that have it (the Paper Pro Move's kernel doesn't; the other layers don't rely on it):
   file and network access limited once more in the kernel

The HTTP client adds network rules, checked for every connection after DNS (so DNS rebinding
can't get around them): **no addresses in your local network or on the tablet** (loopback,
private, link-local, CGNAT, multicast), so a page can't make the browser reach your router or
other devices; and **only web ports** 80, 443, 8080, 8443.

Images are decoded inside the sandbox by Go's memory-safe decoders and re-encoded as new JPEG
files, so the frontend (in xochitl, which runs as root) never opens anything a website sent. The
frontend checks again that images come only from the backend's image folder and links are only
`http(s)`. The image cache is capped at 150 MB. If the sandbox can't be set up, the browser refuses
to run. *Settings* shows the active layers.

## Limits

- No JavaScript means pages that build their content with scripts stay empty (the app says so),
  and there are no logins, comments or forms
- DuckDuckGo sometimes asks to prove you're not a bot (more likely after many quick searches).
  The app then asks you to try again in a few minutes
- Sites on unusual ports (other than 80, 443, 8080, 8443) and addresses in your local network
  (router, NAS, `localhost`) can't be opened

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
| `/home/root/.cache/rm-browser/` | converted images (at most 150 MB) |
| `/home/root/.cache/rm-browser-jail/` | the sandbox's empty root folder (DNS settings only) |

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
  images.go            image download, re-encoding, resizing, optional grey scale
  main.go              messages, history, back/forward, clearing data
tests/qml/             headless frontend tests (stub AppLoad module, Dockerfile)
packaging/vellum/browser/VELBUILD
```

## License

MIT
