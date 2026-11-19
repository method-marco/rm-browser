# rm-browser

Two [AppLoad](https://github.com/asivery/rm-appload) apps for reMarkable tablets that share one
reader (pages you turn, two pages side by side in landscape, the same font panel):

- **Browser** (`rm-browser`): a distraction-free web browser in reader view, below
- **Books** (`rm-books`): an EPUB reader for the books in your library folder "Library", with
  KOReader progress sync, see [Books](#books)


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
- **Two pages in landscape:** turn the tablet sideways and the reader shows two pages side by side,
  like an open book, turned two at a time; turn it back for one page. You stay where you were
  reading. (Save as PDF then makes PDF pages the size of one of these pages)
- **Contents** (like a book's table of contents): the article's headings with their page numbers
  (tap to jump), and the site's navigation menu as a list of links
- **DuckDuckGo** search with **autocomplete** while you type, plus matching pages and searches
  from your history. Results are shown page by page, *More results* loads the next batch.
  Type an address (`road.cc`, `https://…`) to open it directly
- **Save as PDF** (menu): like printing, the page goes into the library folder **Web** exactly as
  the reader shows it: same pages, same lines, same fonts, size and pictures, in real text you can
  select, highlight and search; links work and the headings become the PDF's table of contents. Saving again, e.g. after changing the text size, adds a new document ("Title (2)"),
  nothing is replaced. Needs the [rm-librarian](https://github.com/rmitchellscott/rm-librarian) xovi
  extension (`vellum add librarian`), which adds documents to the library without restarting it
- **Videos** can't play here (no JavaScript, and video makes no sense on e-ink): each one becomes
  a line "*Video:* title" linking to it, so you can watch it on another device (also from a saved PDF)
- **One tab.** *Back*, *Forward* and *Home* in the menu
- **Clear browsing data:** one button (home screen, menu, Settings) deletes history and searches,
  the back/forward list, cookies, and cached pages and images. Settings stay
- **Font and layout** (menu → *Aa Font*, shown at the bottom so the page changes live above it):
  12 typefaces (Noto Sans and EB Garamond from the tablet; Inter, IBM Plex Sans, Atkinson
  Hyperlegible, Literata, Merriweather, Source Serif 4, Lora, Crimson Pro, Bitter and IBM Plex
  Mono bundled, SIL OFL), text size 16 to 96 px, line spacing (1.0 to 2.0), left or justified,
  narrow/normal/wide margins. Saved PDFs use the same
- Settings: images on/off, colour or grey, safe search, insecure (`http://`) pages blocked or allowed
- Images in colour (for the Paper Pro's colour screen) or grey scale (Settings), sized to fit
  the screen; e-ink-friendly UI (black/white,
  no animations, large touch targets); always full screen

## Books

**Books** reads the EPUBs in the reMarkable library folder **"Library"** (and its subfolders), the
ones you add with the reMarkable app, by e-mail or by USB. Nothing is copied: it reads them
where the tablet keeps them, and never changes them. PDFs are not shown.

- **The same reader as the browser:** pages you turn (tap right/left or swipe), no line or picture
  cut at the bottom, two pages side by side in landscape, the same **Font and layout** panel
  (12 typefaces, size, line spacing, left/justified, margins) plus **paragraphs indented** like a
  printed book or spaced. **Book defaults**: Literata 34 px, justified, indented paragraphs;
  *Defaults* in the panel goes back to them
- Chapters follow each other: past the last page of a chapter comes the next one. **Contents**
  (the book's own table of contents, with the share of the book where each entry starts), links
  inside the book (footnotes) with **‹ Back**, page slider, chapter and percentage in the menu
- The library list shows author, subfolder and how far you are; books you read come first. The
  book open when you closed the app opens again at your page
- Pictures are converted like the browser's (colour or grey, sized for the screen, or hidden)
- **KOReader progress sync** ([kosync](https://github.com/koreader/koreader-sync-server)): the
  reading position is shared with KOReader on other devices, the built-in EPUB reader (with
  [kosync-native-reader](https://github.com/method-marco/remarkable-mods)), Readest and other
  kosync apps. Books are matched as KOReader matches them (partial MD5 of the file) and positions
  are KOReader's own (xpointers into the EPUB), so every app lands on the same sentence.
  Opening a book jumps to a newer position from another device (unless you already turned
  pages, then it only says so); turning pages and closing the book send yours. *Sync* in the
  menu does it by hand. Log in under Settings, or nothing to do if kosync-native-reader is set up:
  Books takes over its login once. Only the MD5 key of the password is stored, as KOReader does
- Sandboxed like the browser: its backend can read the library (a read-only handle opened before
  the sandbox), its own two folders and talk to the sync server, nothing else

## Security

Security comes from established libraries, not home-made code, and from never running what a
website sends:

- **No JavaScript, ever.** Pages are only parsed, never executed. (A Chromium engine isn't
  available to AppLoad apps; it would also be the opposite of distraction-free.)
- **HTTPS** with Go's TLS stack: certificates verified (system roots, or Mozilla's root store built
  into the app via `golang.org/x/crypto/x509roots/fallback`), TLS 1.2+ only. Invalid certificates
  are refused. **HTTPS-only** by default: `http://` addresses (typed, linked or redirected to) are
  opened as `https://`; sites without a secure version are refused unless you allow insecure pages in
  Settings (they are then marked *not secure*)
- HTML is cleaned by Readability and then by [bluemonday](https://github.com/microcosm-cc/bluemonday)
  (strict allowlist; links only `http`/`https`). The app then writes its own, minimal rich text
  from that, with all page text escaped

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
`http(s)`. The image cache is capped at 150 MB.

*Save as PDF*: the frontend sends only the reader's layout (per line: how many characters, where);
the backend draws the page from its own copy of it with the embedded fonts
([fpdf](https://codeberg.org/go-pdf/fpdf)) and writes the PDF through a file it opened before
dropping root, in a root-owned folder in RAM (`/run/rm-browser-export`). So it can't make root
import any other file; librarian copies exactly that file into the library, then it is emptied. If the sandbox can't be set up, the browser refuses
to run. *Settings* shows the active layers.

## Privacy

- **No trackers run:** without JavaScript there are no analytics scripts, fingerprinting or ads
- **Tracking pixels and ad images are never downloaded:** images declared tiny (`width="1"`),
  from ~90 ad/analytics domains (Google ads and analytics, Facebook pixel, Taboola, Outbrain, …) or
  with typical pixel names (`spacer.gif`, …) are skipped before any request
- **Cookies live in memory only**, never on the tablet, and are **forgotten when you close the
  browser**, so sites can't recognise you from one use to the next. *Clear browsing data* throws
  away everything else too
- DuckDuckGo's result links are unwrapped, so DuckDuckGo doesn't see which result you open; no
  Referer is sent when opening a page; a common Firefox user agent
- No telemetry. History and the image cache stay on the tablet until you clear them
- Not covered: DNS lookups are not encrypted (your router and provider see which sites, not which
  pages), and autocomplete sends what you type to DuckDuckGo

## Limits

- No JavaScript means pages that build their content with scripts stay empty (the app says so),
  and there are no logins, comments or forms
- DuckDuckGo sometimes asks to prove you're not a bot (more likely after many quick searches).
  The app then asks you to try again in a few minutes
- Sites on unusual ports (other than 80, 443, 8080, 8443) and addresses in your local network
  (router, NAS, `localhost`) can't be opened

## Requirements

- reMarkable Paper Pro Move (tested, OS 3.29) or Paper Pro (should work, untested) with
  [xovi](https://github.com/asivery/xovi) + [AppLoad](https://github.com/asivery/rm-appload).
  reMarkable 1/2 builds exist but are untested, and the sandbox is weaker there (see above)
- Wi-Fi on the tablet

## Install

With [vellum](https://github.com/vellum-dev/vellum), once the package is published:
`vellum add reader-browser` (and `vellum add librarian` for Save as PDF).

By hand: download `rm-browser-aarch64.tar.gz` (Paper Pro / Move) or `rm-browser-armv7.tar.gz`
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
| `/run/rm-browser-export/` | Save as PDF: the PDF while it is being saved (RAM only) |
| `/home/root/.cache/rm-browser-jail/` | the sandbox's empty root folder (DNS settings only) |
| `/home/root/.config/rm-books/config.json` | Books: settings, reading position per book, sync login (user, MD5 key) |
| `/home/root/.cache/rm-books/` | Books: converted pictures (at most 150 MB) |
| `/home/root/.cache/rm-books-jail/` | Books: the sandbox's empty root folder |

Both folders belong to user `nobody` (the sandbox). Cookies are never written to disk.

## Building

Needs Go ≥ 1.26 and Qt 6's `rcc`, or just Docker:

```sh
make docker            # builds dist/*.tar.gz (both apps) and runs the tests in a container
make test              # backend unit tests, incl. a test that the sandbox really blocks things
make qmltest           # frontend tests: headless Qt in Docker with simulated taps and swipes
make install ARCH=aarch64 HOST=10.11.99.1   # build and copy both apps to a device over SSH
make install APPS=rm-books                   # ... or one of them

# check against real sites (DuckDuckGo, Wikipedia, macrumors, engadget, cyclingweekly, road.cc):
cd backend && RM_BROWSER_LIVE=1 go test -run 'Live|Sites' -v ./...
# ... and also write them as fixtures for `make qmltest` (paginates them, checks no line is cut):
RM_BROWSER_LIVE=1 RM_BROWSER_FIXTURE=$PWD/../build/fixture go test -run Sites ./... && (cd ../build/fixture && ls *.json > index.txt)
```

## Layout

```
app/
  shared/              shared by both apps (imported as "../shared")
    ReaderView.qml     the reader: pagination, landscape spreads, taps and swipes, positions
    FontPanel.qml      Font and layout
    fonts.js           the typefaces (keys, families, files)
    EButton.qml Field.qml PagedList.qml FullScreen.qml
  fonts/               bundled typefaces (SIL OFL), in both apps
  browser/             Browser: main.qml (search, address bar, contents, Save as PDF),
                       manifest.json (id "rm-browser"), icon.png (scripts/make_icon.py)
  books/               Books: main.qml (library, chapters, contents, sync login),
                       manifest.json (id "rm-books"), icon.png (scripts/make_books_icon.py)
  browser.qrc books.qrc   Qt resources of each app
backend/               Go -> one static binary per app (no cgo)
  internal/            shared by both backends
    appload/           AppLoad socket protocol
    sandbox/           privilege drop, chroot, Landlock, seccomp allowlist
    store/             config and cache folders (os.Root)
    imaging/           picture decoding, re-encoding, resizing, grey scale
    livedns/           DNS that follows the tablet's network inside the sandbox
    typography/        the Font panel's settings, checked the same way in both apps
  *.go                 the Browser backend:
    fetch.go           HTTP client, HTTPS-only, address-or-search detection
    trackers.go        tracking pixel and ad image filter
    ddg.go             DuckDuckGo search and autocomplete
    reader.go          Readability + bluemonday + Qt rich text writer
    images.go          image download (conversion: internal/imaging)
    export.go          Save as PDF: the reader's layout -> text PDF (fpdf), export folder
    fonts/             the reader's typefaces for the PDF (SIL OFL, see OFL-*.txt)
    main.go            messages, history, back/forward, clearing data
  books/               the Books backend:
    library.go         the folder "Library" in xochitl's documents
    epub.go            EPUB parsing into KOReader's (crengine's) DOM, xpointers, table of
                       contents (from kosync-native-reader's bridge)
    render.go          chapter -> Qt rich text, with each visible character's source position
    book.go            percentages, xpointer <-> reader position
    kosync.go          the sync server client, KOReader's document digest
    main.go            messages, reading positions, automatic sync
tests/qml/             headless frontend tests of both apps (stub AppLoad module, Dockerfiles)
packaging/vellum/      reader-browser/VELBUILD, reader-books/VELBUILD
```

## License

MIT
