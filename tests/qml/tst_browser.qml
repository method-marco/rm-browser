// Headless tests of the frontend with simulated taps and swipes:
//   make qmltest      (screenshots land in build/qmltest/)
import QtQuick 2.15
import QtTest 1.2
import net.asivery.AppLoad 1.0
import net.asivery.XoviMessageBroker 2.0

Item {
    id: win
    width: 954; height: 1696   // reMarkable Paper Pro Move

    Loader { id: loader; anchors.fill: parent; source: "../../app/ui/main.qml" }

    TestCase {
        id: tc
        name: "Browser"
        when: windowShown && loader.status === Loader.Ready
        property var app: loader.item
        property var te: null
        readonly property string outDir: Qt.resolvedUrl("../../build/qmltest/").toString().replace("file://", "")

        function article(paragraphs) {
            let h = ""
            const words = "The quick brown fox jumps over the lazy dog while the e-ink screen keeps everything perfectly still and easy on the eyes. "
            for (let i = 0; i < paragraphs; i++) {
                if (i % 6 === 0) h += "<h2>Section " + (i / 6 + 1) + "</h2>"
                if (i === 9) h += '<p style="line-height:125%">Here is a <a href="https://example.org/linked" style="color:#000000; text-decoration:underline">LINKTARGET link</a> inside text.</p>'
                if (i === 14) h += '<p align="center"><img src="' + Qt.resolvedUrl("../../app/icon.png") + '" width="400" height="400"/></p>'
                if (i === 20) h += '<p style="line-height:125%">Jump to <a href="#sec-3" style="color:#000000; text-decoration:underline">ANCHORLINK</a>.</p>'
                h += '<p style="line-height:125%">' + i + ". " + words.repeat(3 + (i % 4)) + "</p>"
            }
            const headings = []
            for (let i = 0; i < paragraphs; i += 6) headings.push({ level: 2, text: "Section " + (i / 6 + 1), id: "sec-" + (i / 6 + 1) })
            return { url: "https://example.com/story", title: "A Long Test Story", byline: "Test Writer", site: "example.com",
                     html: h, headings: headings, links: [{ text: "News", url: "https://example.com/news" }, { text: "Reviews", url: "https://example.com/reviews" }],
                     secure: true, reader: true }
        }
        function state(extra) {
            return Object.assign({ settings: { fontSize: 32, serif: false, safeSearch: "moderate", noImages: false },
                                   history: [{ url: "https://example.com/old", title: "An old page", time: 1 }, { query: "e-ink tablets", time: 2 }],
                                   canBack: true, canForward: false, imageDir: "/repo/app", sandbox: "unprivileged user, Landlock v3, seccomp", version: "test" }, extra || {})
        }
        function shot(name) {
            let done = false
            app.grabToImage(function (r) { r.saveToFile(outDir + name + ".png"); done = true })
            tryVerify(function () { return done }, 3000)
        }
        function showArticle(pos, n) {
            Bus.deliver(103, { page: article(n || 60), position: pos || 0 })
            tryVerify(function () { return app.pageCount > 1 }, 3000, "paginated")
            wait(100)
        }
        function tapText(needle) {
            const txt = te.getText(0, te.length)
            const i = txt.indexOf(needle)
            verify(i >= 0, needle + " in text")
            const r = te.positionToRectangle(i + 2)
            const p = te.mapToItem(app, r.x + 4, r.y + r.height / 2)
            mouseClick(app, p.x, p.y)
        }

        function initTestCase() {
            tryVerify(function () { return Bus.lastOf(1) !== null }, 2000, "init sent")
            const init = Bus.lastOf(1)
            verify(init.width > 700 && init.height > 1000, "viewport " + JSON.stringify(init))
            Bus.deliver(101, state())
            te = findChild(app, "te")
            verify(te)
        }

        function test_01_home() {
            Bus.deliver(107, {})
            compare(app.view, "home")
            shot("home")
            mouseClick(app, win.width / 2, 170)           // the search field
            compare(app.panel, "entry")
            Bus.clear()
            keyClick(Qt.Key_E); keyClick(Qt.Key_Minus); keyClick(Qt.Key_I)
            tryVerify(function () { return Bus.lastOf(4) !== null }, 2000, "suggest requested")
            compare(Bus.lastOf(4).q, "e-i")
            Bus.deliver(105, { q: "e-i", items: ["e-ink display", "e-ink tablet", "e-ink monitor"] })
            // history match first ("e-ink tablets" contains "e-i"), then suggestions
            compare(app.entryModel[0].value, "e-ink tablets")
            compare(app.entryModel[1].value, "e-ink display")
            shot("entry")
            mouseClick(app, 200, 32 + 80 + 14 + 84 + 42)   // second row
            compare(Bus.lastOf(2).input, "e-ink display")
            compare(app.panel, "")
        }

        function test_02_pagination_no_cut_lines() {
            showArticle(0)
            const H = Math.floor(findChild(app, "readerArea").height)
            verify(app.pageCount >= 5, "pages: " + app.pageCount)
            for (let i = 0; i < app.pageCount; i++) {
                verify(app.pageHeights[i] <= H, "page " + i + " height " + app.pageHeights[i] + " > " + H)
                verify(app.pageHeights[i] > H * 0.5 || i === app.pageCount - 1, "page " + i + " is mostly empty")
                if (i === 0) continue
                const s = app.pageStarts[i]
                const r = te.positionToRectangle(te.positionAt(4, s + 1))
                verify(r.y >= s - 1, "page " + i + " starts inside a line: line y " + r.y + " < start " + s)
                compare(app.pageStarts[i - 1] + app.pageHeights[i - 1], s)
            }
            shot("page1")
        }

        function test_03_tap_and_swipe() {
            showArticle(0)
            Bus.clear()
            mouseClick(app, win.width - 60, win.height / 2)
            compare(app.pageIndex, 1)
            compare(Bus.lastOf(7).page, 1)
            mouseClick(app, 60, win.height / 2)
            compare(app.pageIndex, 0)
            // finger swipe to the left = next page
            mouseDrag(app, win.width * 0.8, win.height / 2, -400, 10)
            compare(app.pageIndex, 1)
            mouseDrag(app, win.width * 0.2, win.height / 2, 400, 10)
            compare(app.pageIndex, 0)
            // real touch input, too
            const t = touchEvent(app)
            t.press(0, app, win.width - 80, 900).commit()
            t.release(0, app, win.width - 80, 900).commit()
            compare(app.pageIndex, 1)
        }

        function test_04_link_tap() {
            showArticle(0)
            const txt = te.getText(0, te.length)
            const i = txt.indexOf("LINKTARGET")
            app.goToPage(app.pageOfY(te.positionToRectangle(i).y))
            wait(50)
            Bus.clear()
            tapText("LINKTARGET")
            compare(Bus.lastOf(2).input, "https://example.org/linked")
        }

        function test_05_anchor_link_and_contents() {
            showArticle(0)
            verify(app.headingPages.length === 10, "heading pages " + app.headingPages)
            for (let i = 1; i < app.headingPages.length; i++)
                verify(app.headingPages[i] >= app.headingPages[i - 1] && app.headingPages[i] >= 0, "order " + app.headingPages)
            const txt = te.getText(0, te.length)
            app.goToPage(app.pageOfY(te.positionToRectangle(txt.indexOf("ANCHORLINK")).y))
            wait(50)
            tapText("ANCHORLINK")
            compare(app.pageIndex, app.headingPages[2])

            // contents panel via the menu
            app.goToPage(0)
            mouseClick(app, win.width / 2, win.height / 2)
            verify(app.menuOpen)
            shot("menu")
            app.contentsTab = "headings"
            app.panel = "contents"; app.menuOpen = false
            wait(50)
            shot("contents")
            // rows start below title, tabs and page title; tap "Section 5" (row 5: Beginning is row 0)
            const rowY = 40 + 68 + 18 + 68 + 18 + 30 + 18 + 84 * 5 + 42
            mouseClick(app, 300, rowY)
            compare(app.panel, "")
            compare(app.pageIndex, app.headingPages[4])
        }

        function test_06_font_size_keeps_position() {
            showArticle(0)
            app.goToPage(4)
            const before = app.topCharPos
            const pagesBefore = app.pageCount
            Bus.deliver(101, state({ settings: { fontSize: 44, serif: true, safeSearch: "moderate", noImages: false } }))
            tryVerify(function () { return app.pageCount > pagesBefore }, 3000, "more pages with larger text")
            const y = te.positionToRectangle(before).y
            verify(y >= app.pageStarts[app.pageIndex] - 1 && y < app.pageStarts[app.pageIndex] + app.pageHeights[app.pageIndex],
                   "reading position kept: char y " + y + " page " + app.pageIndex)
            shot("page-serif-44")
            Bus.deliver(101, state())
        }

        function test_07_restore_position() {
            showArticle(3)
            compare(app.pageIndex, 3)
        }

        function test_08_search_results() {
            const res = []
            for (let i = 0; i < 25; i++)
                res.push({ title: "Result number " + i + " about e-ink displays and paper tablets", url: "https://site" + i + ".example/page",
                           host: "site" + i + ".example", snippet: "A snippet of text describing result " + i + ", long enough to wrap onto a second line on the tablet screen." })
            Bus.deliver(104, { query: "e-ink display", results: res, hasMore: true, append: false, page: 0 })
            compare(app.view, "search")
            wait(100)
            shot("search")
            Bus.clear()
            mouseClick(app, 300, 40 + 80 + 18 + 214 + 60)   // second result
            compare(Bus.lastOf(2).input, "https://site1.example/page")
            // go to the last page, then "More results"
            let guard = 0
            while (Bus.lastOf(3) === null && guard++ < 10)
                mouseDrag(app, win.width * 0.8, 800, -400, 10)
            verify(Bus.lastOf(3) !== null, "more results requested")
        }

        function test_09_clear_browsing() {
            Bus.deliver(107, {})
            app.panel = "settings"
            wait(50)
            shot("settings")
            app.panel = "confirm"
            wait(50)
            shot("confirm")
            Bus.clear()
            // "Clear everything" button: centre of the dialog, left button
            const dlgTop = win.height / 2
            let sent = false
            for (let y = dlgTop - 100; y < dlgTop + 250 && !sent; y += 20) {
                mouseClick(app, win.width / 2 - 250, y)
                sent = Bus.lastOf(8) !== null
                if (app.panel !== "confirm") break
            }
            verify(sent, "clear data sent")
        }

        function test_11_unsafe_html_dropped() {
            const h = app.safeHtml('<p>a<img src="file:///home/root/.local/share/remarkable/xochitl/x.png" width="10" height="10"/>'
                + '<img src="file:///repo/app/../../etc/x.jpg"/><img src="file:///repo/app/icon.png" width="5" height="5"/>'
                + '<table background="file:///etc/x.png"><tr><td style="background-image: url(file:///x)">c</td></tr></table>'
                + '<link rel="stylesheet" href="x.css"><script>x</script></p>')
            verify(h.indexOf("xochitl") < 0 && h.indexOf("etc/x") < 0 && h.indexOf("url(") < 0 && h.indexOf("<link") < 0 && h.indexOf("<script") < 0, h)
            verify(h.indexOf('src="file:///repo/app/icon.png"') > 0, h)
            Bus.clear()
            app.openLink("file:///etc/passwd")
            app.openLink("javascript:alert(1)")
            compare(Bus.lastOf(2), null)
        }

        function test_10_error_view() {
            Bus.deliver(102, { message: "cannot find example.invalid (no Wi-Fi, or the address is wrong)", retry: true })
            compare(app.view, "error")
            shot("error")
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
        function tapButton(label) {
            const b = findButton(app, label)
            verify(b, "button " + label)
            const p = b.mapToItem(app, b.width / 2, b.height / 2)
            mouseClick(app, p.x, p.y)
        }
        function exportOnce(expectName) {
            app.menuOpen = true
            wait(50)
            Bus.clear()
            tapButton("Save as PDF")
            verify(app.exportStatus.indexOf("Saving PDF") === 0, app.exportStatus)
            tryVerify(function () { return Bus.lastOf(15) !== null }, 5000, "layout sent")
            const req = Bus.lastOf(15)
            compare(req.url, "https://example.com/story")
            compare(req.width, win.width); compare(req.height, win.height)
            compare(req.x, 56); compare(req.y, 48)
            compare(req.fontSize, 32)
            compare(req.pageStarts.length, app.pageCount)
            compare(req.lines.length % 4, 0)
            // every visible character of the page is on exactly one line
            let sum = 0, prevY = -1, ordered = true
            for (let i = 0; i < req.lines.length; i += 4) {
                sum += req.lines[i]
                if (req.lines[i + 1] < prevY && req.lines[i + 3] === 0) ordered = false
                prevY = req.lines[i + 1]
            }
            const te = findChild(app, "te")
            compare(sum, Array.from(te.getText(0, te.length).replace(/\s/g, "")).length)
            verify(ordered, "lines in reading order")
            compare(app.pageIndex, 2, "still on the same page")
            shot("pdf-saving")
            Bus.deliver(109, { path: outDir + "export/Web page.pdf", title: "A Long Test Story" })
            verify(!app.exporting && app.exportStatus === "", app.exportStatus)
            compare(Library.docs[Library.docs.length - 1], expectName)
            compare(Library.imported[Library.imported.length - 1], outDir + "export/Web page.pdf")
            verify(app.toast.indexOf("Saved to Web: " + expectName) === 0, app.toast)
            verify(Bus.lastOf(16) !== null, "export file emptied")
        }

        function test_12_save_pdf() {
            Library.reset()
            Bus.deliver(101, state({ exportDir: outDir + "export" }))
            showArticle(2, 30)
            exportOnce("A Long Test Story")
            exportOnce("A Long Test Story (2)")          // a new version, never replaced
            compare(Library.docs.length, 2)

            // answers nobody asked for are ignored
            const calls = Library.calls.length
            Bus.deliver(109, { path: outDir + "export/Web page.pdf", title: "Injected" })
            compare(Library.calls.length, calls)

            // a path other than the export file is refused
            app.menuOpen = true; wait(50); Bus.clear()
            tapButton("Save as PDF")
            tryVerify(function () { return Bus.lastOf(15) !== null }, 20000)
            Bus.deliver(109, { path: "/home/root/.ssh/id_rsa", title: "x" })
            verify(app.exportStatus.indexOf("Could not save") === 0, app.exportStatus)
            compare(Library.imported.length, 2)
            tapButton("OK")
            compare(app.exportStatus, "")

            // without rm-librarian: a clear message, nothing captured
            Library.installed = false
            app.menuOpen = true; wait(50); Bus.clear()
            tapButton("Save as PDF")
            verify(app.exportStatus.indexOf("rm-librarian") > 0, app.exportStatus)
            compare(Bus.lastOf(15), null)
            shot("pdf-no-librarian")
            tapButton("OK")
            Library.installed = true
        }
    }
}
