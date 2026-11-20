// Headless tests of the Books frontend (app/books/main.qml) with simulated
// taps: make qmltest (screenshots land in build/qmltest/books-*.png)
import QtQuick 2.15
import QtTest 1.2
import net.asivery.AppLoad 1.0

Item {
    id: win
    property int keysOut: 0
    Keys.onPressed: keysOut++
    Keys.onReleased: keysOut++
    width: 954; height: 1696   // reMarkable Paper Pro Move

    // stands for xochitl's screens under the app: no tap may reach it
    MouseArea { id: below; anchors.fill: parent; property int taps: 0; onPressed: taps++; onClicked: taps++ }
    Loader { id: loader; anchors.fill: parent; source: "../../app/books/main.qml" }

    TestCase {
        id: tc
        name: "Books"
        when: windowShown && loader.status === Loader.Ready
        property var app: loader.item
        property var te: null
        property var reader: null
        readonly property string outDir: Qt.resolvedUrl("../../build/qmltest/").toString().replace("file://", "")

        function read(url) {
            const x = new XMLHttpRequest()
            x.open("GET", url, false)
            x.send()
            return x.responseText
        }
        function shot(name) {
            let done = false
            app.grabToImage(function (r) { r.saveToFile(outDir + "books-" + name + ".png"); done = true })
            tryVerify(function () { return done }, 3000)
        }
        function findButton(item, label) {
            if (!item) return null
            if (item.visible && item.text === label && item.clicked !== undefined) return item
            for (let i = 0; i < item.children.length; i++) {
                const f = findButton(item.children[i], label)
                if (f) return f
            }
            return null
        }
        function findText(item, t) {
            if (!item) return null
            if (item.visible && item.text === t && item.font !== undefined) return item
            for (let i = 0; i < item.children.length; i++) {
                const f = findText(item.children[i], t)
                if (f) return f
            }
            return null
        }
        function tapButton(label) {
            const b = findButton(app, label)
            verify(b, "button " + label)
            const p = b.mapToItem(app, b.width / 2, b.height / 2)
            mouseClick(app, p.x, p.y)
        }
        function state(extra) {
            return Object.assign({ settings: { fontSize: 34, font: "literata", lineSpacing: 125, align: "justify", margin: "normal", paragraphs: "indent",
                                               noImages: false, greyImages: false, bookStyle: true },
                                   defaults: { fontSize: 34, font: "literata", lineSpacing: 125, align: "justify", margin: "normal", paragraphs: "indent", bookStyle: true },
                                   sync: { server: "https://sync.example.org", username: "ada", loggedIn: true, auto: true, device: "reMarkable Books" },
                                   version: "test", sandbox: "unprivileged user, seccomp", imageDir: "/repo/app/books", folder: "Library" }, extra || {})
        }
        // a long chapter: paragraphs with numbered words, so any position is recognizable
        function chapterHtml(n, paras) {
            let h = "<h2>Chapter " + (n + 1) + "</h2>"
            for (let i = 0; i < paras; i++) {
                if (i === 7) h += '<p style="line-height:125%">See <a href="book:2#notes" style="color:#000000; text-decoration:underline">LINKNOTE</a> here.</p>'
                h += '<p style="line-height:125%">'
                for (let w = 0; w < 40; w++) h += "c" + n + "p" + i + "w" + w + " "
                h += "</p>"
            }
            return h
        }
        function openTestBook() {
            Bus.deliver(104, { id: "b1", title: "A Test Book", author: "Ada Writer", chapters: 3, counts: [5000, 0, 3000],
                               toc: [{ title: "One", level: 1, chapter: 0, char: 0 }, { title: "Two", level: 1, chapter: 2, char: 0 },
                                     { title: "Notes", level: 2, chapter: 2, char: 2000 }], imageBook: false })
        }
        function deliverChapter(n, ch, extra) {
            Bus.deliver(103, Object.assign({ book: "b1", chapter: n, html: chapterHtml(n, ch || 40), char: 0, end: false }, extra || {}))
            tryVerify(function () { return reader.pageCount > 1 && reader.pageHtml !== "" }, 3000, "paginated")
            wait(150)
        }

        function initTestCase() {
            tryVerify(function () { return Bus.lastOf(1) !== null }, 2000, "init sent")
            const init = Bus.lastOf(1)
            verify(init.width > 700 && init.height > 1000, "viewport " + JSON.stringify(init))
            Bus.deliver(101, state())
            te = findChild(app, "te")
            reader = findChild(app, "readerView")
            verify(reader && reader.setContent !== undefined, "reader view")
        }

        function test_01_library() {
            Bus.deliver(105, { found: true, folder: "Library", books: [
                { id: "b1", title: "A Test Book", author: "Ada Writer", folder: "", percent: 0.37, read: 1700000000 },
                { id: "b2", title: "Another Book", author: "", folder: "Novels", percent: 0, read: 0 }] })
            compare(app.view, "library")
            verify(findText(app, "37%"), "progress shown")
            verify(findText(app, "New"), "unread book")
            verify(findText(app, "Novels"), "subfolder shown")
            shot("library")
            Bus.clear()
            const row = findText(app, "Another Book")
            const p = row.mapToItem(app, 10, row.height / 2)
            const before = below.taps
            mouseClick(app, p.x, p.y)
            compare(Bus.lastOf(2).id, "b2")
            compare(below.taps, before, "the tap reached the screen behind the app")
            // no Library folder / empty
            Bus.deliver(105, { found: false, folder: "Library", books: [] })
            verify(findText(app, "There is no folder \u201cLibrary\u201d in your library. Make one (in the library or the reMarkable app) and put EPUB books in it."), "no Library folder")
            shot("library-missing")
        }

        // The backend's positions (n-th visible character) must point at the
        // same character in Qt's text: the fixture comes from the Go writer
        // (backend/books/fixture_test.go).
        function test_02_counting_matches_backend() {
            const fx = JSON.parse(read(Qt.resolvedUrl("books-chapter.json")))
            Bus.deliver(101, state({ imageDir: fx.imageDir }))
            openTestBook()
            Bus.deliver(103, { book: "b1", chapter: 0, html: fx.html, char: 0, end: false })
            tryVerify(function () { return reader.pageHtml !== "" && te.length > 100 }, 3000)
            wait(200)
            const all = te.getText(0, te.length)
            const exact = all.length === te.length
            console.log("text length", te.length, "getText", all.length, exact ? "exact" : "tables differ")
            compare(reader.charIndexAt(te.length), fx.count, "visible characters in the chapter")
            for (const s of fx.samples) {
                const pos = reader.positionOfChar(s.k)
                const got = te.getText(pos, pos + s.ch.length)
                compare(got, s.ch, "character " + s.k)
                compare(reader.charIndexAt(pos), s.k, "index of character " + s.k)
            }
            shot("tricky")
            Bus.deliver(101, state())
        }

        function test_03_open_at_position_and_turn_chapters() {
            openTestBook()
            // the saved position: character 3000 of chapter 0 is on the page shown
            deliverChapter(0, 40, { char: 3000 })
            const pos = reader.positionOfChar(3000)
            const pg = reader.pageOfY(te.positionToRectangle(pos).y)
            compare(reader.pageIndex, pg, "the page with the saved position")
            verify(reader.pageIndex > 0)
            verify(reader.pageHtml.indexOf("text-indent") > 0, "book paragraphs: indented")
            // room around headings in text units, no indent right after one
            verify(reader.pageHtml.indexOf('<h2 style="margin-top:61px; margin-bottom:34px">') === 0, reader.pageHtml.substring(0, 120))
            verify(/<\/h2><p align="justify" style="line-height:125%; text-indent:0px/.test(reader.pageHtml), "first paragraph not indented")
            verify(/<\/p><p align="justify" style="line-height:125%; text-indent:48px/.test(reader.pageHtml), "the next one is")
            verify(te.text.indexOf('align="justify"') > 0, "justified")
            shot("page")
            // the footer: page and percentage of the book
            verify(String(findChild(app, "pageFooter").text).indexOf("%") > 0)

            // turning the page reports the position (first visible character on screen)
            Bus.clear()
            mouseClick(app, win.width - 60, win.height / 2)
            compare(Bus.lastOf(4).chapter, 0)
            compare(Bus.lastOf(4).char, reader.topChar())
            verify(Bus.lastOf(4).char > 3000)

            // past the last page: the next chapter (the backend skips empty ones)
            reader.goToPage(reader.pageCount - 1)
            Bus.clear()
            mouseClick(app, win.width - 60, win.height / 2)
            compare(Bus.lastOf(3).chapter, 1)
            compare(Bus.lastOf(3).dir, 1)
            Bus.clear()
            deliverChapter(2, 30)
            compare(reader.pageIndex, 0)
            tryVerify(function () { return Bus.lastOf(4) !== null }, 2000, "new chapter reported")
            compare(Bus.lastOf(4).chapter, 2)
            compare(app.chapter, 2)
            // back before the first page: the previous chapter, at its end
            Bus.clear()
            mouseClick(app, 60, win.height / 2)
            compare(Bus.lastOf(3).chapter, 1)
            compare(Bus.lastOf(3).char, -1)
            compare(Bus.lastOf(3).dir, -1)
            deliverChapter(0, 40, { char: -1, end: true })
            compare(reader.pageIndex, reader.pageCount - 1, "at the end of the previous chapter")
        }

        function test_04_links_contents_back() {
            openTestBook()
            deliverChapter(0, 40)
            const txt = te.getText(0, te.length)
            const i = txt.indexOf("LINKNOTE")
            reader.goToPage(reader.pageOfY(te.positionToRectangle(i).y))
            wait(50)
            Bus.clear()
            const r = te.positionToRectangle(i + 2)
            const p = te.mapToItem(app, r.x + 4, r.y + r.height / 2)
            mouseClick(app, p.x, p.y)
            compare(Bus.lastOf(5).href, "book:2#notes")
            compare(app.backStack.length, 1)
            deliverChapter(2, 30, { char: 600 })
            mouseClick(app, win.width / 2, win.height / 2)
            verify(app.menuOpen)
            verify(findButton(app, "‹ Back"), "back after a link")
            shot("menu")
            Bus.clear()
            tapButton("‹ Back")
            compare(Bus.lastOf(3).chapter, 0)
            compare(app.backStack.length, 0)
            deliverChapter(0, 40)

            // contents: entries with their share of the book; tap one
            app.menuOpen = true; wait(50)
            tapButton("Contents")
            compare(app.panel, "contents")
            verify(findText(app, "Notes"), "toc entry")
            shot("contents")
            Bus.clear()
            const row = findText(app, "Notes")
            const q = row.mapToItem(app, 10, row.height / 2)
            mouseClick(app, q.x, q.y)
            compare(app.panel, "")
            compare(Bus.lastOf(3).chapter, 2)
            compare(Bus.lastOf(3).char, 2000)
        }

        function test_05_sync_positions() {
            openTestBook()
            deliverChapter(0, 40)
            // a newer position from another device, nothing read yet: go there
            Bus.clear()
            Bus.deliver(108, { chapter: 2, char: 1200, percent: 0.8, device: "Kobo", manual: false })
            compare(Bus.lastOf(3).chapter, 2)
            compare(Bus.lastOf(3).char, 1200)
            verify(app.toast.indexOf("Kobo") > 0, app.toast)
            deliverChapter(2, 30, { char: 1200 })
            // after turning pages here: only a note (the reader's own place wins)
            mouseClick(app, win.width - 60, win.height / 2)
            Bus.clear()
            Bus.deliver(108, { chapter: 0, char: 10, percent: 0.01, device: "Phone", manual: false })
            compare(Bus.lastOf(3), null)
            verify(app.toast.indexOf("Phone is at 1%") === 0, app.toast)
            // Sync in the menu asks the backend
            app.menuOpen = true; wait(50)
            Bus.clear()
            tapButton("Sync")
            verify(Bus.lastOf(8) !== null)
        }

        function findTextWith(item, part) {
            if (!item) return null
            if (item.visible && item.font !== undefined && typeof item.text === "string" && item.text.indexOf(part) >= 0
                && item.clicked === undefined && item.objectName !== "te" && item.objectName !== "te2") return item
            for (let i = 0; i < item.children.length; i++) {
                const f = findTextWith(item.children[i], part)
                if (f) return f
            }
            return null
        }
        function tapItem(it) {
            verify(it, "item to tap")
            const p = it.mapToItem(app, it.width / 2, it.height / 2)
            mouseClick(app, p.x, p.y)
        }
        // visible characters before a word of the test chapter
        function charOfWord(w) {
            const all = te.getText(0, te.length)
            const i = all.indexOf(w + " ")
            verify(i >= 0, "word " + w)
            return reader.charIndexAt(i)
        }

        // hyphenation (backend/books/hyphenate.go): a row in the Font panel;
        // soft hyphens break words at line ends and are not counted
        function test_06e_hyphenation() {
            Bus.deliver(101, state())
            openTestBook()
            let h = "<p>"
            for (let i = 0; i < 60; i++) h += "Ex\u00ADtra\u00ADor\u00ADdi\u00ADnar\u00ADi\u00ADly in\u00ADter\u00ADna\u00ADtion\u00ADal w" + i + " "
            Bus.deliver(103, { book: "b1", chapter: 0, html: h + "</p>", char: 0, end: false })
            tryVerify(function () { return reader.pageHtml !== "" && te.length > 100 }, 3000)
            wait(200)
            compare(reader.charIndexAt(te.length), 60 * ("Extraordinarilyinternationalw".length) + 10 + 50 * 2, "soft hyphens not counted")
            shot("hyphenation")
            app.toast = ""
            app.panel = "font"; wait(50)
            verify(findButton(app, "On").active, "on by default")
            Bus.clear()
            tapButton("Off")
            compare(Bus.lastOf(6).hyphenate, false)
            Bus.deliver(101, state({ settings: Object.assign({}, state().settings, { hyphenate: false }) }))
            verify(findButton(app, "Off").active)
            fontPanelClose()
            Bus.deliver(101, state())
        }

        // search (backend/books/search.go): the panel, the list, the match
        // shown selected, the bar's ‹ › ×, and ‹ Back to where it started
        function test_10_search() {
            Bus.deliver(101, state())
            openTestBook()
            deliverChapter(0, 30)
            app.toast = ""
            const start = reader.topChar()
            app.menuOpen = true; wait(50)
            tapButton("Search")
            compare(app.panel, "search")
            const field = findChild(app, "searchField")
            verify(findTextWith(app, "Case and accents"), "hint")
            field.text = "c0p20w3"
            Bus.clear()
            tapButton("Search")
            compare(Bus.lastOf(15).query, "c0p20w3")
            verify(findTextWith(app, "Searching"), "searching")
            const k = charOfWord("c0p20w3")
            Bus.deliver(109, { book: "b1", query: "c0p20w3", total: 2, hits: [
                { chapter: 0, char: k, len: 7, before: "… c0p20w1 c0p20w2 ", match: "c0p20w3", after: " c0p20w4 c0p20w5 …", title: "One", percent: 0.1 },
                { chapter: 2, char: 8, len: 6, before: "a <b> Chapter 3 ", match: "c2p0w0", after: " & more", title: "Two", percent: 0.7 } ] })
            verify(findTextWith(app, "2 matches for"), "count")
            verify(findTextWith(app, "One  ·  10%"), "where")
            wait(100)
            shot("search-list")
            Bus.clear()
            tapItem(findTextWith(app, "c0p20w2"))
            compare(app.panel, "")
            compare(app.hitIndex, 0)
            tryVerify(function () { return te.selectedText === "c0p20w3" }, 2000, "the match selected")
            verify(reader.topChar() <= k && reader.positionOfChar(k) < te.positionAt(4, reader.pageStarts[reader.pageIndex] + reader.pageHeights[reader.pageIndex] - 2),
                   "the match on screen")
            verify(findChild(app, "searchBar").visible)
            verify(findButton(app, "1 / 2   \u201Cc0p20w3\u201D"), "the bar")
            wait(200)
            shot("search-match")
            // the next match: another chapter
            tapButton("\u203A")
            compare(Bus.lastOf(3).chapter, 2)
            compare(Bus.lastOf(3).char, 8)
            Bus.deliver(103, { book: "b1", chapter: 2, html: chapterHtml(2, 30), char: 8, end: false })
            tryVerify(function () { return te.selectedText === "c2p0w0" }, 3000, "selected in chapter 2")
            verify(findButton(app, "2 / 2   \u201Cc0p20w3\u201D"))
            // the bar's label opens the list again; × ends it
            tapButton("2 / 2   \u201Cc0p20w3\u201D")
            compare(app.panel, "search")
            compare(findChild(app, "searchField").text, "c0p20w3")
            tapButton("\u00D7")
            compare(app.panel, "")
            tapButton("\u00D7")
            verify(!findChild(app, "searchBar").visible)
            compare(te.selectedText, "")
            compare(reader.highlight, null)
            // ‹ Back: where the search started
            compare(app.backStack.length, 1)
            compare(app.backStack[0].chapter, 0)
            compare(app.backStack[0].char, start)
            // another book: no matches left over
            openTestBook()
            compare(app.searchHits.length, 0)
        }

        function test_06_font_panel_defaults_and_paragraphs() {
            Bus.deliver(101, state({ settings: { fontSize: 40, font: "sans", lineSpacing: 150, align: "left", margin: "wide", paragraphs: "spaced" } }))
            openTestBook()
            deliverChapter(0, 40, { char: 2000 })
            verify(reader.pageHtml.indexOf("text-indent") < 0, "spaced paragraphs")
            app.menuOpen = true; wait(50)
            tapButton("Aa  Font")
            compare(app.panel, "font")
            app.toast = ""                                // a notice covers the bottom of the panel
            verify(findButton(app, "Indented"), "paragraph style row")
            shot("font-panel")
            Bus.clear()
            tapButton("Indented")
            compare(Bus.lastOf(6).paragraphs, "indent")
            tapButton("Defaults")
            compare(Bus.lastOf(6).font, "literata")
            compare(Bus.lastOf(6).paragraphs, "indent")
            const keep = reader.topChar()
            Bus.deliver(101, state())
            tryVerify(function () { return reader.pageHtml.indexOf("text-indent") > 0 && te.font.family === "Literata" }, 3000)
            wait(500)
            // still at the same place in the text
            const top = reader.topChar()
            const pos = reader.positionOfChar(keep)
            const pg = reader.pageOfY(te.positionToRectangle(pos).y)
            verify(pg === reader.pageIndex || pg === reader.pageIndex + 1 || Math.abs(top - keep) < 400, "position kept: " + keep + " -> " + top)
            verify(!findButton(app, "Defaults").enabled, "at the defaults")
            mouseClick(app, win.width / 2, 100)
            compare(app.panel, "")
        }

        // the book's own formatting: em becomes pixels for the text size
        function test_06b_book_style() {
            Bus.deliver(101, state())
            openTestBook()
            const html = '<h2 align="center" style="line-height:125%; font-size:1.5em">Title</h2>'
                       + '<p style="line-height:125%; text-indent:1.5em; margin-top:0em; margin-bottom:0.5em">One <span style="font-size:0.8em">small</span> 2em text.</p>'
                       + chapterHtml(0, 30)
            Bus.deliver(103, { book: "b1", chapter: 0, html: html, char: 0 })
            tryVerify(function () { return reader.pageHtml !== "" && reader.pageCount > 1 }, 3000)
            verify(reader.pageHtml.indexOf("text-indent:51px") > 0, "1.5em at 34 px")
            verify(reader.pageHtml.indexOf("font-size:51px") > 0 && reader.pageHtml.indexOf("font-size:27px") > 0, "sizes")
            verify(reader.pageHtml.indexOf("margin-bottom:17px") > 0, "spacing")
            verify(reader.pageHtml.indexOf("One ") > 0 && reader.pageHtml.indexOf(" 2em text.") > 0, "text itself unchanged")
            shot("book-style")
            Bus.deliver(101, state({ settings: { fontSize: 40, font: "literata", lineSpacing: 125, align: "justify", margin: "normal", paragraphs: "indent", bookStyle: true } }))
            tryVerify(function () { return reader.pageHtml.indexOf("text-indent:60px") > 0 }, 3000, "follows the text size")
            // the Formatting row
            app.toast = ""
            app.panel = "font"; wait(50)
            verify(findButton(app, "Book's").active, "book's style on")
            Bus.clear()
            tapButton("Plain")
            compare(Bus.lastOf(6).bookStyle, false)
            tapButton("Defaults")
            compare(Bus.lastOf(6).bookStyle, true)
            app.panel = ""
            Bus.deliver(101, state())
        }

        // themes (a dropdown in the Font panel): Default and Night shift
        function fontPanelClose() { app.panel = ""; wait(50) }
        function themed(extra) { return state({ settings: Object.assign({}, state().settings, extra) }) }
        function test_06c_themes() {
            Bus.deliver(101, state())
            openTestBook()
            deliverChapter(0, 30)
            compare(String(app.color), "#ffffff")
            app.toast = ""
            app.panel = "font"; wait(50)
            verify(!findButton(app, "Night shift"), "list closed")
            verify(!findText(app, "At night"), "no evening row")
            tapButton("Default  \u25BE")
            wait(50)                                     // the panel lays out the list
            verify(!findButton(app, "A+"), "the list replaces the other rows")
            compare(String(findButton(app, "Night shift").color), "#e8c994", "shown in its own colours")
            Bus.clear()
            tapButton("Night shift")
            compare(Bus.lastOf(6).theme, "nightshift")
            verify(findButton(app, "A+") && !findButton(app, "Night shift"), "list closed after the choice")
            Bus.deliver(101, themed({ theme: "nightshift" }))
            // the text is set again although it stays black: the tablet
            // redraws only what changed, the paper hid the unchanged text
            compare(reader.pageHtml, "", "text set again")
            tryCompare(app, "color", "#e8c994")
            compare(String(te.color), "#000000", "black text")
            compare(String(findButton(app, "Left").color), "#e8c994", "buttons on the warm paper")
            verify(findButton(app, "Night shift  \u25BE"), "the dropdown shows the theme")
            tryVerify(function () { return reader.pageHtml !== "" }, 3000)
            wait(200)
            shot("theme-nightshift")
            app.panel = ""
            // soft dark: light grey on dark grey, links included
            app.panel = "font"; wait(50)
            tapButton("Night shift  \u25BE")
            wait(50)
            verify(findButton(app, "Sepia") && findButton(app, "Cream") && findButton(app, "Dark"), "the earlier themes")
            Bus.clear()
            tapButton("Soft dark")
            compare(Bus.lastOf(6).theme, "softdark")
            Bus.deliver(101, themed({ theme: "softdark" }))
            tryCompare(app, "color", "#3a3a3a")
            compare(String(te.color), "#e6e6e6")
            tryVerify(function () { return reader.pageHtml.indexOf("color:#e6e6e6") > 0 && reader.pageHtml.indexOf("color:#000000") < 0 }, 3000, "links")
            compare(String(findButton(app, "Justified").color), "#e6e6e6", "active button inverted")
            tryVerify(function () { return reader.pageHtml !== "" }, 3000)
            wait(200)
            shot("theme-softdark")
            tapButton("Soft dark  \u25BE")
            wait(50)
            shot("theme-list")
            fontPanelClose()
            // Defaults leaves the colours alone
            verify(state().defaults.theme === undefined)
            Bus.deliver(101, state())
            tryCompare(app, "color", "#ffffff")
        }

        function test_07_settings_and_login() {
            app.panel = "settings"; wait(50)
            verify(findText(app, "Logged in as ada on sync.example.org."), "login shown")
            shot("settings")
            Bus.clear()
            tapButton("Push")
            verify(Bus.lastOf(14) !== null, "push")
            tapButton("Pull")
            verify(Bus.lastOf(13) !== null, "pull")
            compare(app.panel, "", "the panel closes: the page shows where the pull goes")
            // the answer: the server's position, even an older one, is shown
            Bus.clear()
            Bus.deliver(108, { chapter: 0, char: 100, percent: 0.02, device: "this tablet", manual: true })
            compare(Bus.lastOf(3).chapter, 0)
            compare(Bus.lastOf(3).char, 100)
            app.panel = "settings"; wait(50)
            Bus.clear()
            tapButton("Off")
            compare(Bus.lastOf(6).autoSync, false)
            tapButton("Change login")
            compare(app.panel, "login")
            shot("login")
            win.keysOut = 0
            keyClick(Qt.Key_End); keyClick(Qt.Key_X)                // typed into the user name field
            Bus.clear()
            tapButton("Log in")
            const m = Bus.lastOf(9)
            compare(m.username, "adax")
            compare(m.server, "https://sync.example.org")
            verify(m.password === undefined, "no password typed: the saved one is kept")
            compare(win.keysOut, 0, "keys went on to xochitl")
            compare(app.panel, "settings")
            Bus.deliver(101, state({ sync: { server: "https://sync.koreader.rocks", username: "", loggedIn: false, auto: true } }))
            verify(findText(app, "Not logged in. Reading positions stay on this tablet."))
            app.panel = ""
            Bus.deliver(101, state())
        }

        function test_08_taps_never_fall_through() {
            openTestBook()
            deliverChapter(0, 30)
            const before = below.taps
            for (const p of [[20, 300], [win.width / 2, win.height - 300], [win.width - 20, win.height / 2], [win.width / 2, win.height - 5], [5, 5]]) {
                mouseClick(app, p[0], p[1])
                wait(20)
                app.menuOpen = false
                app.panel = ""
            }
            Bus.deliver(102, { message: "Could not open the book: broken" })
            Bus.deliver(105, { found: true, folder: "Library", books: [] })
            mouseClick(app, win.width / 2, win.height / 2)
            compare(below.taps, before, "a tap fell through to the screen under the app")
        }

        // landscape: two pages, turned two at a time, chapters still continue
        function test_09_landscape() {
            Bus.deliver(101, state())
            openTestBook()
            deliverChapter(0, 40, { char: 2500 })
            win.width = 1696; win.height = 954
            tryVerify(function () { return reader.spread && reader.area.width < win.width / 2 }, 2000)
            wait(400)
            tryVerify(function () { return reader.pageIndex % 2 === 0 }, 2000)
            const te2 = findChild(app, "te2")
            verify(te2, "right page")
            shot("landscape")
            Bus.clear()
            const before = reader.pageIndex
            mouseClick(app, win.width - 60, win.height / 2)
            compare(reader.pageIndex, before + 2)
            reader.goToPage(reader.pageCount - 1)
            Bus.clear()
            mouseClick(app, win.width - 60, win.height / 2)
            compare(Bus.lastOf(3).chapter, 1)
            win.width = 954; win.height = 1696
            tryVerify(function () { return !reader.spread }, 2000)
        }

        // pixels on the page that aren't paper (every 6th in each direction)
        function inkPixels() {
            const img = grabImage(app)
            const ra = findChild(app, "readerArea")
            const o = ra.mapToItem(app, 0, 0)
            const paper = String(app.color)
            let n = 0
            for (let y = o.y; y < o.y + ra.height; y += 6)
                for (let x = o.x; x < o.x + ra.width; x += 6)
                    if (String(img.pixel(x, y)) !== paper) n++
            return n
        }
        // Deep in a long chapter, new text at the same scroll position stayed
        // (partly) blank: TextEdit draws only the blocks in its viewport.
        function test_06d_redraw_deep_in_a_long_chapter() {
            Bus.deliver(101, state())
            openTestBook()
            Bus.deliver(103, { book: "b1", chapter: 0, html: chapterHtml(0, 900), char: 0, end: false })
            tryVerify(function () { return reader.pageCount > 60 && reader.pageHtml !== "" }, 10000, "paginated")
            wait(200)
            reader.goToPage(64); wait(200)
            const top = reader.topChar(), full = inkPixels()
            verify(full > 2000, "text on the page: " + full)
            const steps = [{ theme: "nightshift" }, { theme: "softdark" }, { theme: "default" }, { align: "left" }, { align: "justify" }]
            for (const st of steps) {
                Bus.deliver(101, themed(st))
                wait(100)
                tryVerify(function () { return reader.pageHtml !== "" && reader.pageCount > 60 }, 3000)
                wait(300)
                compare(reader.topChar(), top, "same page after " + JSON.stringify(st))
                const n = inkPixels()
                verify(n > full * 0.8, JSON.stringify(st) + ": " + n + " of " + full + " pixels drawn")
            }
            Bus.deliver(101, state())
        }

    }
}
