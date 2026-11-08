// rm-browser frontend (AppLoad / Qt Quick). A distraction-free, reader-only
// browser for e-ink: one tab, every page in reader view, split into pages
// that you turn instead of scrolling. Black on white, no animations, large
// touch targets.
import QtQuick 2.15
import QtQuick.Controls 2.15
import QtQuick.Layouts 1.15
import net.asivery.AppLoad 1.0

Rectangle {
    id: root
    anchors.fill: parent
    color: "white"

    // Required by AppLoad.
    signal close
    // Called by AppLoad when the app closes (it may call it twice). Don't
    // call backend.terminate() here: terminating from inside the unload
    // makes AppLoad unload the frontend a second time, which crashes
    // xochitl ("double free or corruption"). AppLoad reuses the running
    // backend when the app is opened again.
    function unloading() {
        cleanUpForClose()
        // forget cookies: sites can't recognise you the next time
        try { send(reqClosed, {}) } catch (e) {}
    }
    // No text field may keep the keyboard and no timer may fire into a
    // half-destroyed app.
    function cleanUpForClose() {
        suggestTimer.stop()
        paginateTimer.stop()
        setTextTimer.stop()
        viewportTimer.stop()
        toastTimer.stop()
        exportTimer.stop()
        exporting = false
        exportStatus = ""
        Qt.inputMethod.hide()
        root.forceActiveFocus()
    }
    function closeApp() {
        cleanUpForClose()
        closeTimer.start()   // let the keyboard detach before AppLoad tears the app down
    }
    Timer { id: closeTimer; interval: 200; onTriggered: root.close() }

    // ---- protocol (must match backend/main.go) ----
    readonly property int reqInit: 1
    readonly property int reqOpen: 2
    readonly property int reqSearchMore: 3
    readonly property int reqSuggest: 4
    readonly property int reqBack: 5
    readonly property int reqForward: 6
    readonly property int reqPosition: 7
    readonly property int reqClearData: 8
    readonly property int reqSettings: 9
    readonly property int reqHome: 10
    readonly property int reqReload: 11
    readonly property int reqCancel: 12
    readonly property int reqViewport: 13
    readonly property int reqClosed: 14
    readonly property int reqSavePDF: 15
    readonly property int reqExported: 16

    property var appState: ({ settings: { fontSize: 32, serif: false, safeSearch: "moderate", noImages: false, greyImages: false, allowHTTP: false },
                              history: [], canBack: false, canForward: false, sandbox: "", version: "" })
    readonly property var settings: appState.settings || {}
    property string view: "home"        // home | page | search | error
    property bool menuOpen: false
    property string panel: ""           // "" | entry | contents | settings | confirm
    property string busyText: ""
    property string errorText: ""
    property bool errorRetry: false
    property string toast: ""

    // reader page
    property var pageData: null
    property string pageHtml: ""
    property var pageStarts: [0]        // y of each screen page in the text
    property var pageHeights: [1]       // visible height of each page (no line is cut in half)
    property int pageIndex: 0
    property int pendingPosition: -1    // page to show once the text is laid out
    property int topCharPos: -1         // first character on screen: kept when the text reflows
    property var headingPages: []       // page of each heading, -1 = not found
    readonly property int pageCount: pageStarts.length

    // search results
    property string searchQuery: ""
    property var results: []
    property bool hasMore: false

    // address bar
    property var suggestions: []
    property string suggestFor: ""
    property var entryModel: []
    property string contentsTab: "headings"

    function send(type, obj) { backend.sendMessage(type, JSON.stringify(obj || {})) }
    function esc(t) { return String(t || "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;") }
    function hostOf(u) { const m = String(u || "").match(/^https?:\/\/([^\/?#]+)/i); return m ? m[1].replace(/^www\./, "") : "" }

    AppLoad {
        id: backend
        applicationID: "rm-browser"
        onMessageReceived: (type, contents) => {
            let d = {}
            try { d = contents ? JSON.parse(contents) : {} } catch (e) { return }
            switch (type) {
            case 101:
                root.appState = d
                break
            case 102:
                busyText = ""
                if (d.retry) {
                    errorText = d.message
                    errorRetry = true
                    view = "error"
                    menuOpen = false
                } else {
                    showToast(d.message)
                }
                break
            case 103: showPage(d.page, d.position || 0); break
            case 104:
                if (d.append) {
                    results = results.concat(d.results || [])
                } else {
                    searchQuery = d.query
                    results = d.results || []
                    resultsList.page = 0
                    resultsList.pendingPage = d.page || 0
                }
                hasMore = !!d.hasMore
                view = "search"
                menuOpen = false
                if (panel !== "settings") panel = ""
                Qt.callLater(resultsList.applyPending)
                break
            case 105:
                if (d.q === entryField.text.trim()) {
                    suggestions = d.items || []
                    suggestFor = d.q
                    updateEntryModel()
                }
                break
            case 106: busyText = d.busy ? d.text : ""; break
            case 107:
                view = "home"
                pageData = null
                pageHtml = ""
                menuOpen = false
                if (panel !== "settings" && panel !== "confirm") panel = ""
                break
            case 108: showToast(d.message); break
            case 109: pdfReady(d); break
            }
        }
    }

    Component.onCompleted: {
        fullscreenOnly()
        initTimer.start()
    }
    // Wait one moment so that the reader area has its size: the backend
    // sizes images to fit it.
    Timer {
        id: initTimer
        interval: 50
        onTriggered: send(reqInit, viewport())
    }
    function viewport() {
        return { width: Math.floor(readerArea.width), height: Math.floor(readerArea.height * 0.8) }
    }

    // Reading only makes sense full screen, so hide the minimise and
    // maximise buttons of AppLoad's window bar (only close stays). AppLoad
    // offers this through the manifest only for native apps, so find our
    // AppLoad window up the parent chain and set its flag ourselves. If a
    // future AppLoad changes this, nothing is found and nothing happens.
    property var appWindow: null
    function fullscreenOnly() {
        for (let p = root.parent; p; p = p.parent) {
            if (typeof p.disablesWindowedMode === "boolean") {
                appWindow = p
                p.disablesWindowedMode = true
                fullscreenCheck.start()
                return
            }
        }
    }
    Timer {
        id: fullscreenCheck
        interval: 1500
        onTriggered: {
            if (appWindow && appWindow.fullscreen === false && typeof appWindow.maximize === "function")
                appWindow.maximize()
        }
    }

    Timer { id: toastTimer; interval: 4000; onTriggered: toast = "" }
    function showToast(t) { toast = t; toastTimer.restart() }

    // ------------------------------------------------------------ save as PDF
    // Works like printing: the PDF gets the same pages with the same lines
    // as the reader, in real text. The frontend sends how Qt laid the page
    // out (per line: visible characters, position, height; where pages
    // start), the backend draws it with the same fonts and saves it as
    // "<name>.pdf", and rm-librarian (a xovi extension) files it in the
    // library folder "Web", named after the file. Saving again makes a new
    // document ("Title (2)", ...), it never replaces one.
    // Only librarian calls that don't wait for xochitl's main thread may be
    // used here (ensureFolder, lookupEntry, importDocument): this code runs
    // on it, so e.g. renameEntry would deadlock the whole tablet UI.
    property bool exporting: false
    property string exportStatus: ""     // full-screen message while saving, or an error
    property string exportName: ""      // document name being saved
    property var librarianObj: null
    readonly property string libraryFolder: "Web"

    function librarian() {
        if (!librarianObj) {
            try {
                librarianObj = Qt.createQmlObject("import net.asivery.XoviMessageBroker 2.0\nXoviMessageBroker {}", root, "librarian")
            } catch (e) {
                librarianObj = null
            }
        }
        return librarianObj
    }
    function libCall(signal, arg) {
        const b = librarian()
        const r = b ? String(b.sendSimpleSignal(signal, arg) || "").trim() : ""
        if (r === "") throw new Error("Saving PDFs to the library needs the rm-librarian extension. Install it with vellum (vellum add librarian), then restart the tablet's interface.")
        if (r.indexOf("ERROR") === 0) throw new Error(r.replace(/^ERROR:?\s*/, ""))
        return r
    }
    // A page title as document (and file) name: one line, no "/", at most
    // 180 bytes so that " (12).pdf" still fits a file name.
    function docName(t) {
        let n = String(t || "").replace(/[\/\\]/g, "-")
                               .replace(/[\u0000-\u001f\u007f-\u009f\u2028\u2029]/g, " ").replace(/\s+/g, " ").trim()
        n = n.replace(/^\.+/, "").trim()
        const bytes = function (x) { return unescape(encodeURIComponent(x)).length }
        if (bytes(n) > 180) {
            let cps = Array.from(n)
            while (cps.length > 0 && bytes(cps.join("") + "\u2026") > 180) cps.pop()
            n = cps.join("").trim() + "\u2026"
        }
        return n || "Web page"
    }
    function exportFail(msg) {
        exporting = false
        exportStatus = "Could not save the PDF.\n\n" + msg
        send(reqExported)
    }
    function startExport() {
        if (exporting || view !== "page" || !pageData || !appState.exportDir) return
        try {
            libCall("ensureFolder", libraryFolder)   // fails early if librarian is missing
            const base = docName(pageData.title)
            let name = base
            for (let k = 2; k < 1000; k++) {      // a new version, never replace
                let exists = true
                try { libCall("lookupEntry", libraryFolder + "/" + name) } catch (e) { exists = false }
                if (!exists) break
                name = base + " (" + k + ")"
            }
            exportName = name
        } catch (e) {
            exportStatus = "Could not save the PDF.\n\n" + e.message
            return
        }
        exporting = true
        exportStatus = "Saving PDF \u2026"
        exportTimer.restart()    // let the message show first
    }
    Timer {
        id: exportTimer
        interval: 50
        onTriggered: {
            if (!exporting) return
            const t0 = Date.now()
            const req = exportLayout()
            console.log("rm-browser: page layout read in", Date.now() - t0, "ms,", req.lines.length / 4, "lines")
            send(reqSavePDF, req)
        }
    }

    // How Qt laid out the page: for every line on screen the number of
    // visible characters (pictures count one), its y, height and x.
    // The text is read once and cut by position: getText() of a range that
    // touches a table returns the whole table (Qt 6.10), and table cells are
    // marked with U+FDD0/U+FDD1, which don't count.
    function exportLayout() {
        const lines = []
        const len = te.length
        const all = te.getText(0, len)
        const exact = all.length === len
        const markRe = /[\ufdd0-\ufdd2]/
        const piece = function (a, b) { return exact ? all.slice(a, b) : te.getText(a, b) }
        const visible = function (t) { return Array.from(t.replace(/[\s\ufdd0-\ufdd2\ufeff]/g, "")).length }
        let p = 0, lastY = -1
        for (let guard = 0; p < len && guard < 200000; guard++) {
            const r = te.positionToRectangle(p)
            // the end of this line; never skip a character: where a line
            // breaks after a hyphen, Qt's line end is before the hyphen,
            // which then becomes a piece of its own (drawn at its own x)
            let e = te.positionAt(te.width + 1000, r.y + r.height / 2)
            if (e <= p) e = p + 1
            // in tables: stop at the cell's end, and at the cell's next line
            if (exact) {
                const m = all.slice(p, e).search(markRe)
                if (m === 0) e = p + 1
                else if (m > 0) {
                    e = p + m
                    const q = te.positionAt(r.x + 1, r.y + r.height + 1)
                    if (q > p && q < e && te.positionToRectangle(q).y > r.y) e = q
                }
            }
            const vis = visible(piece(p, e))
            if (vis > 0 || r.y !== lastY) {   // empty lines and rules count, trailing blanks don't
                lines.push(vis, Math.round(r.y * 2) / 2, Math.round(r.height * 2) / 2, Math.round(r.x))
                lastY = r.y
            }
            p = e
        }
        return { name: exportName, url: pageData.url || "", fontSize: settings.fontSize || 32, serif: !!settings.serif,
                 width: Math.round(root.width), height: Math.round(root.height),
                 x: Math.round(readerArea.x), y: Math.round(readerArea.y),
                 lines: lines, pageStarts: pageStarts, pageHeights: pageHeights }
    }
    // the backend's answer: import the PDF into the library
    function pdfReady(d) {
        if (!exporting) return            // only answers to our own request
        if (d.error) { exportFail(d.error); return }
        if (d.name !== exportName || d.path !== appState.exportDir + "/" + exportName + ".pdf") { exportFail("Unexpected file."); return }
        try {
            const folder = libCall("ensureFolder", libraryFolder).split("\n")[0]
            libCall("importDocument", d.path + "," + folder)
        } catch (e) {
            exportFail(e.message)
            return
        }
        exporting = false
        exportStatus = ""
        send(reqExported)
        showToast("Saved to " + libraryFolder + ": " + exportName)
    }

    // ------------------------------------------------------------ navigation
    function openInput(text) {
        const t = String(text || "").trim()
        if (t === "") return
        closePanel()
        menuOpen = false
        send(reqOpen, { input: t })
    }
    function openLink(href) {
        if (!href) return
        if (href.charAt(0) !== "#" && !/^https?:\/\//i.test(href)) return   // web links only
        if (href.charAt(0) === "#") {
            const id = decodeURIComponent(href.substring(1))
            const hs = pageData ? pageData.headings : []
            for (let i = 0; i < hs.length; i++)
                if (hs[i].id === id && headingPages[i] >= 0) { goToPage(headingPages[i]); return }
            return
        }
        openInput(href)
    }
    function openEntry() {
        let t = ""
        if (view === "page" && pageData) t = pageData.url
        else if (view === "search") t = searchQuery
        entryField.text = t
        suggestions = []
        suggestFor = ""
        updateEntryModel()
        panel = "entry"
        menuOpen = false
        entryField.forceActiveFocus()
        entryField.selectAll()
    }
    function closePanel() {
        if (panel === "entry") {
            suggestTimer.stop()
            Qt.inputMethod.hide()
            root.forceActiveFocus()
        }
        panel = ""
    }

    // ------------------------------------------------------------ reader
    // Defence in depth: the backend already writes only safe rich text, but
    // xochitl (which shows it) runs as root, so check again here: images
    // only from the backend's image folder, nothing else Qt could load.
    function safeHtml(h) {
        const dir = appState.imageDir ? "file://" + appState.imageDir + "/" : ""
        return String(h || "")
            .replace(/<img\b[^>]*>/gi, function (tag) {
                const m = tag.match(/\bsrc="([^"]*)"/i)
                return dir !== "" && m && m[1].indexOf(dir) === 0 && m[1].indexOf("..") < 0 ? tag : ""
            })
            .replace(/\bbackground\s*=\s*"[^"]*"/gi, "")
            .replace(/url\s*\(/gi, "")
            .replace(/<(link|style|script|iframe|object|embed)\b[^>]*>/gi, "")
    }
    function showPage(p, position) {
        pageData = p
        let meta = []
        if (p.site) meta.push(esc(p.site))
        if (p.byline) meta.push(esc(p.byline))
        if (!p.secure) meta.push("<b>not secure (http)</b>")
        if (!p.reader) meta.push("simplified page")
        pendingPosition = position
        topCharPos = -1
        pageIndex = 0
        pageStarts = [0]
        pageHeights = [readerArea.height]
        headingPages = []
        // Qt's TextEdit doesn't redraw correctly when its rich text is
        // replaced in place (parts of the new page stay blank, old images
        // linger). Clearing it first and setting the new text a moment
        // later is reliable.
        pageHtml = ""
        nextHtml = "<h1>" + esc(p.title) + "</h1><p><small>" + meta.join(" · ") + "</small></p>" + safeHtml(p.html)
        setTextTimer.restart()
        view = "page"
        menuOpen = false
        if (panel !== "settings") closePanel()
        schedulePaginate()
    }

    property string nextHtml: ""
    Timer {
        id: setTextTimer
        interval: 30
        onTriggered: { pageHtml = nextHtml; nextHtml = ""; schedulePaginate() }
    }
    Timer { id: paginateTimer; interval: 60; onTriggered: paginate() }
    function schedulePaginate() { if (view === "page") paginateTimer.restart() }

    // Splits the laid-out text into screen pages. A page ends where the
    // next one starts; a line (or image) that would be cut by the bottom
    // edge moves to the next page instead.
    function paginate() {
        if (view !== "page" || pageHtml === "" || readerArea.height < 100) return
        const H = Math.floor(readerArea.height)
        const total = te.contentHeight
        if (total < 10) return   // not laid out yet
        const starts = [0], heights = []
        let t = 0
        for (let guard = 0; guard < 10000; guard++) {
            const b = t + H
            if (b >= total - 1) { heights.push(Math.max(1, Math.ceil(total - t))); break }
            let next = b
            const r = te.positionToRectangle(te.positionAt(4, b))
            if (r.y < b && r.y + r.height > b && r.y > t + 1) next = Math.floor(r.y)
            heights.push(next - t)
            starts.push(next)
            t = next
        }
        let target = 0
        if (pendingPosition >= 0) { target = pendingPosition; pendingPosition = -1 }
        else if (topCharPos >= 0) target = pageOfY(te.positionToRectangle(topCharPos).y, starts)
        pageStarts = starts
        pageHeights = heights
        pageIndex = Math.max(0, Math.min(target, starts.length - 1))
        computeHeadingPages()
        recordTop()
    }
    function pageOfY(y, starts) {
        const s = starts || pageStarts
        let i = 0
        while (i + 1 < s.length && s[i + 1] <= y + 1) i++
        return i
    }
    function recordTop() {
        topCharPos = te.positionAt(4, pageStarts[pageIndex] + 2)
    }
    // Finds each heading in the laid-out text to know its page.
    function computeHeadingPages() {
        const res = []
        if (!pageData) { headingPages = res; return }
        const txt = te.getText(0, te.length)
        let from = (pageData.title || "").length
        for (const h of pageData.headings || []) {
            const i = txt.indexOf(h.text, from)
            if (i < 0) { res.push(-1); continue }
            from = i + h.text.length
            res.push(pageOfY(te.positionToRectangle(i).y))
        }
        headingPages = res
    }
    function goToPage(i) {
        const n = Math.max(0, Math.min(i, pageCount - 1))
        if (n === pageIndex) return
        pageIndex = n
        recordTop()
        send(reqPosition, { page: n })
    }
    function nextPage() { goToPage(pageIndex + 1) }
    function prevPage() { goToPage(pageIndex - 1) }

    // The link under a finger: also look a little around the touch point,
    // fingers are not as exact as a mouse.
    function linkAt(item, x, y) {
        const pt = item.mapToItem(te, x, y)
        const top = pageStarts[pageIndex], bottom = top + pageHeights[pageIndex]
        const offsets = [[0, 0], [0, -14], [0, 14], [-14, 0], [14, 0]]
        for (const o of offsets) {
            const ty = pt.y + o[1]
            if (ty < top || ty > bottom) continue
            const l = te.linkAt(pt.x + o[0], ty)
            if (l) return l
        }
        return ""
    }
    function setFont(size, serif) {
        recordTop()
        send(reqSettings, { fontSize: size, serif: serif })
    }

    // ------------------------------------------------------------ address bar
    Timer {
        id: suggestTimer
        interval: 300
        onTriggered: send(reqSuggest, { q: entryField.text.trim() })
    }
    function updateEntryModel() {
        const q = entryField.text.trim().toLowerCase()
        const out = []
        const hist = appState.history || []
        const typedSame = q !== "" && pageData && q === String(pageData.url).toLowerCase()
        for (const h of hist) {
            if (out.length >= (q === "" || typedSame ? 6 : 3)) break
            const hay = ((h.url || "") + " " + (h.title || "") + " " + (h.query || "")).toLowerCase()
            if (q !== "" && !typedSame && hay.indexOf(q) < 0) continue
            out.push(h.query ? { kind: "search", label: h.query, sub: "Searched before", value: h.query }
                             : { kind: "history", label: h.title || h.url, sub: h.url, value: h.url })
        }
        if (q !== "" && suggestFor.toLowerCase() === q) {
            let n = 0
            for (const s of suggestions) {
                if (n >= 6) break
                if (out.some(o => o.value.toLowerCase() === s.toLowerCase())) continue
                out.push({ kind: "suggest", label: s, sub: "", value: s })
                n++
            }
        }
        entryModel = out
    }
    onAppStateChanged: if (panel === "entry") updateEntryModel()

    // ------------------------------------------------------------ widgets
    component EButton: Rectangle {
        id: btn
        property alias text: lbl.text
        property bool active: false
        property int fontSize: 28
        signal clicked
        implicitWidth: Math.max(100, lbl.implicitWidth + 36)
        implicitHeight: 68
        color: active ? "black" : "white"
        border.color: "black"; border.width: 3; radius: 8
        opacity: enabled ? 1 : 0.3
        Text { id: lbl; anchors.centerIn: parent; font.pixelSize: btn.fontSize; color: btn.active ? "white" : "black" }
        MouseArea { anchors.fill: parent; onClicked: btn.clicked() }
    }

    // A list shown page by page (no scrolling): as many rows as fit, with
    // buttons to turn the page. Swiping left/right turns it too.
    component PagedList: ColumnLayout {
        id: pl
        property var items: []
        property int rowHeight: 90
        property Component rowDelegate
        property int page: 0
        property int pendingPage: -1
        property bool moreAvailable: false
        property string emptyText: ""
        signal moreRequested
        signal pageTurned(int page)
        spacing: 8
        readonly property int perPage: Math.max(1, Math.floor(listArea.height / rowHeight))
        readonly property int pages: Math.max(1, Math.ceil(items.length / perPage))
        onPagesChanged: if (page > pages - 1) page = pages - 1
        function applyPending() {
            if (pendingPage >= 0 && listArea.height > 0) {
                page = Math.min(pendingPage, pages - 1)
                pendingPage = -1
            }
        }
        function turn(d) {
            const n = page + d
            if (n >= 0 && n < pages) { page = n; pageTurned(n) }
            else if (n >= pages && moreAvailable) moreRequested()
        }
        Item {
            id: listArea
            Layout.fillWidth: true
            Layout.fillHeight: true
            clip: true
            onHeightChanged: pl.applyPending()
            Column {
                width: parent.width
                Repeater {
                    model: pl.items.slice(pl.page * pl.perPage, (pl.page + 1) * pl.perPage)
                    delegate: pl.rowDelegate
                }
            }
            Text {
                visible: pl.items.length === 0 && pl.emptyText !== ""
                text: pl.emptyText
                width: parent.width
                wrapMode: Text.Wrap
                font.pixelSize: 26; color: "#444"
            }
            // swipe to turn; taps go through to the rows
            MouseArea {
                anchors.fill: parent
                property real px: 0
                property real py: 0
                propagateComposedEvents: true
                onPressed: (m) => { px = m.x; py = m.y; m.accepted = true }
                onReleased: (m) => {
                    const dx = m.x - px, dy = m.y - py
                    if (Math.abs(dx) > 80 && Math.abs(dx) > Math.abs(dy)) { pl.turn(dx < 0 ? 1 : -1); return }
                    if (Math.abs(dx) > 30 || Math.abs(dy) > 30) return
                    const col = listArea.children[0]
                    const p = mapToItem(col, m.x, m.y)
                    const row = col.childAt(p.x, p.y)
                    if (row && typeof row.tapped === "function") row.tapped()
                }
            }
        }
        RowLayout {
            Layout.fillWidth: true
            visible: pl.pages > 1 || pl.moreAvailable
            spacing: 12
            EButton { text: "‹ Previous"; enabled: pl.page > 0; onClicked: pl.turn(-1) }
            Text {
                Layout.fillWidth: true
                horizontalAlignment: Text.AlignHCenter
                text: (pl.page + 1) + " / " + pl.pages + (pl.moreAvailable ? "+" : "")
                font.pixelSize: 26
            }
            EButton {
                text: pl.page < pl.pages - 1 || !pl.moreAvailable ? "Next ›" : "More results ›"
                enabled: pl.page < pl.pages - 1 || pl.moreAvailable
                onClicked: pl.turn(1)
            }
        }
    }

    component Field: Rectangle {
        id: fld
        property string text: ""
        property string placeholder: ""
        signal clicked
        implicitHeight: 80
        Layout.fillWidth: true
        border.color: "black"; border.width: 3; radius: 10
        Text {
            anchors.fill: parent; anchors.leftMargin: 20; anchors.rightMargin: 20
            verticalAlignment: Text.AlignVCenter
            text: fld.text !== "" ? fld.text : fld.placeholder
            color: fld.text !== "" ? "black" : "#666"
            font.pixelSize: 28
            elide: Text.ElideRight
        }
        MouseArea { anchors.fill: parent; onClicked: fld.clicked() }
    }

    // ------------------------------------------------------------ reader view
    // Where the article is drawn (also measured while hidden: the backend
    // sizes images to fit it).
    Item {
        id: readerArea
        objectName: "readerArea"
        anchors.fill: parent
        anchors.leftMargin: 56; anchors.rightMargin: 56
        anchors.topMargin: 48; anchors.bottomMargin: 76
        visible: view === "page"
        onHeightChanged: { schedulePaginate(); viewportTimer.restart() }
        onWidthChanged: viewportTimer.restart()

        // A Flickable (not interactive: pages are turned by taps) shows one
        // page of the text. Moving the TextEdit inside a plain clipped item
        // does not work: TextEdit only draws the blocks in its viewport and
        // only notices viewport changes made through a Flickable.
        Flickable {
            id: clipBox
            width: parent.width
            height: pageHeights[pageIndex] || parent.height
            clip: true
            interactive: false
            contentWidth: width
            contentHeight: te.contentHeight
            contentY: pageStarts[pageIndex] || 0
            TextEdit {
                id: te
                objectName: "te"
                width: readerArea.width
                readOnly: true
                selectByMouse: false
                activeFocusOnPress: false
                textFormat: TextEdit.RichText
                wrapMode: TextEdit.Wrap
                color: "black"
                font.pixelSize: settings.fontSize || 32
                font.family: settings.serif ? "EB Garamond" : "Noto Sans"
                text: pageHtml
                onContentHeightChanged: schedulePaginate()
                onWidthChanged: schedulePaginate()
                onFontChanged: schedulePaginate()
            }
        }
    }
    Timer {
        id: viewportTimer
        interval: 500
        onTriggered: if (!initTimer.running) send(reqViewport, viewport())
    }

    // taps and swipes on the page: left third back, right third forward,
    // middle opens the menu; links win over page turns
    MouseArea {
        anchors.fill: parent
        visible: view === "page" && !menuOpen && panel === ""
        property real px: 0
        property real py: 0
        onPressed: (m) => { px = m.x; py = m.y }
        onReleased: (m) => {
            const dx = m.x - px, dy = m.y - py
            if (Math.abs(dx) > 80 && Math.abs(dx) > Math.abs(dy)) { if (dx < 0) nextPage(); else prevPage(); return }
            if (Math.abs(dx) > 30 || Math.abs(dy) > 30) return
            const link = linkAt(this, m.x, m.y)
            if (link) { openLink(link); return }
            if (m.x < width / 3) prevPage()
            else if (m.x > width * 2 / 3) nextPage()
            else menuOpen = true
        }
    }

    Text {   // page number
        visible: view === "page"
        anchors.bottom: parent.bottom; anchors.bottomMargin: 22
        anchors.horizontalCenter: parent.horizontalCenter
        text: (pageIndex + 1) + " / " + pageCount
        font.pixelSize: 22; color: "#555"
    }

    // ------------------------------------------------------------ home view
    ColumnLayout {
        visible: view === "home"
        anchors.fill: parent
        anchors.margins: 40
        spacing: 22

        RowLayout {
            Layout.fillWidth: true
            spacing: 12
            Text { text: "Browser"; font.pixelSize: 46; font.bold: true; Layout.fillWidth: true }
            EButton { text: "Settings"; onClicked: panel = "settings" }
            EButton { text: "Close"; onClicked: closeApp() }
        }
        Field { placeholder: "Search DuckDuckGo or type an address"; onClicked: openEntry() }
        RowLayout {
            Layout.fillWidth: true
            visible: !!appState.canForward
            EButton { text: "Forward ›"; onClicked: send(reqForward) }
        }
        Text {
            text: "Recent"
            visible: (appState.history || []).length > 0
            font.pixelSize: 30; font.bold: true
        }
        PagedList {
            id: historyList
            Layout.fillWidth: true
            Layout.fillHeight: true
            items: appState.history || []
            rowHeight: 104
            emptyText: "Nothing here yet. Pages open in reader view: tap the right side of the screen (or swipe left) for the next page, the left side to go back, and the middle for the menu."
            rowDelegate: Item {
                width: historyList.width
                height: historyList.rowHeight
                function tapped() { openInput(modelData.url || modelData.query) }
                Column {
                    anchors.left: parent.left; anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    spacing: 4
                    Text {
                        width: parent.width
                        text: modelData.query ? "Search: " + modelData.query : (modelData.title || modelData.url)
                        font.pixelSize: 28; elide: Text.ElideRight
                    }
                    Text {
                        width: parent.width
                        text: modelData.query ? "DuckDuckGo" : hostOf(modelData.url)
                        font.pixelSize: 22; color: "#555"; elide: Text.ElideRight
                    }
                }
                Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: "#999" }
            }
        }
        EButton {
            Layout.alignment: Qt.AlignHCenter
            text: "Clear browsing data"
            onClicked: panel = "confirm"
        }
    }

    // ------------------------------------------------------------ search results
    ColumnLayout {
        visible: view === "search"
        anchors.fill: parent
        anchors.margins: 40
        spacing: 18

        RowLayout {
            Layout.fillWidth: true
            spacing: 12
            Field { text: searchQuery; onClicked: openEntry() }
            EButton { text: "Menu"; onClicked: menuOpen = true }
        }
        PagedList {
            id: resultsList
            Layout.fillWidth: true
            Layout.fillHeight: true
            items: results
            rowHeight: 214
            moreAvailable: hasMore && page >= pages - 1
            emptyText: "No results."
            onMoreRequested: send(reqSearchMore)
            onPageTurned: (p) => send(reqPosition, { page: p })
            onItemsChanged: {
                // "More results" appended a batch: show it
                if (items.length > page * perPage + perPage && pendingPage < 0 && view === "search" && busyText === "" && page === pages - 2)
                    turn(1)
            }
            rowDelegate: Item {
                width: resultsList.width
                height: resultsList.rowHeight
                function tapped() { openInput(modelData.url) }
                Column {
                    anchors.left: parent.left; anchors.right: parent.right
                    anchors.top: parent.top; anchors.topMargin: 14
                    spacing: 6
                    Text {
                        width: parent.width
                        text: modelData.title
                        font.pixelSize: 29; font.bold: true
                        wrapMode: Text.Wrap; maximumLineCount: 2; elide: Text.ElideRight
                    }
                    Text { width: parent.width; text: modelData.host; font.pixelSize: 22; color: "#444"; elide: Text.ElideRight }
                    Text {
                        width: parent.width
                        text: modelData.snippet
                        font.pixelSize: 23
                        wrapMode: Text.Wrap; maximumLineCount: 3; elide: Text.ElideRight
                    }
                }
                Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: "#999" }
            }
        }
    }

    // ------------------------------------------------------------ error view
    ColumnLayout {
        visible: view === "error"
        anchors.centerIn: parent
        width: parent.width - 120
        spacing: 24
        Text { text: "The page could not be loaded"; font.pixelSize: 36; font.bold: true; Layout.fillWidth: true; wrapMode: Text.Wrap }
        Text { text: errorText; font.pixelSize: 28; Layout.fillWidth: true; wrapMode: Text.Wrap }
        RowLayout {
            spacing: 16
            EButton { text: "Try again"; visible: errorRetry; onClicked: send(reqReload) }
            EButton { text: "‹ Back"; enabled: !!appState.canBack; onClicked: send(reqBack) }
            EButton { text: "Home"; onClicked: send(reqHome) }
            EButton { text: "Menu"; onClicked: menuOpen = true }
        }
    }

    // ------------------------------------------------------------ menu
    MouseArea {   // tap outside the menu closes it
        anchors.fill: parent
        visible: menuOpen
        onClicked: menuOpen = false
    }
    Rectangle {
        id: menuTop
        visible: menuOpen
        anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top
        height: menuCol.implicitHeight + 48
        color: "white"
        Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 3; color: "black" }
        MouseArea { anchors.fill: parent }   // don't close when tapping between buttons
        ColumnLayout {
            id: menuCol
            anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top
            anchors.margins: 24
            spacing: 16
            RowLayout {
                Layout.fillWidth: true
                spacing: 12
                EButton { text: "‹ Back"; enabled: !!appState.canBack; onClicked: send(reqBack) }
                EButton { text: "Forward ›"; enabled: !!appState.canForward; onClicked: send(reqForward) }
                EButton { text: "Home"; onClicked: send(reqHome) }
                Item { Layout.fillWidth: true }
                EButton { text: "Close"; onClicked: closeApp() }
            }
            Field {
                text: view === "page" && pageData ? pageData.url : (view === "search" ? searchQuery : "")
                placeholder: "Search DuckDuckGo or type an address"
                onClicked: openEntry()
            }
            Flow {
                Layout.fillWidth: true
                spacing: 12
                EButton {
                    text: "Contents"
                    visible: view === "page"
                    onClicked: { contentsTab = (pageData && pageData.headings.length === 0 && pageData.links.length > 0) ? "links" : "headings"; panel = "contents"; menuOpen = false }
                }
                EButton { text: "A-"; visible: view === "page"; enabled: (settings.fontSize || 32) > 22; onClicked: setFont((settings.fontSize || 32) - 4, !!settings.serif) }
                EButton { text: "A+"; visible: view === "page"; enabled: (settings.fontSize || 32) < 56; onClicked: setFont((settings.fontSize || 32) + 4, !!settings.serif) }
                EButton { text: settings.serif ? "Sans" : "Serif"; visible: view === "page"; onClicked: setFont(settings.fontSize || 32, !settings.serif) }
                EButton { text: "Save as PDF"; visible: view === "page" && !!appState.exportDir; onClicked: { menuOpen = false; startExport() } }
                EButton { text: "Reload"; visible: view !== "home"; onClicked: { menuOpen = false; send(reqReload) } }
                EButton { text: "Settings"; onClicked: { panel = "settings"; menuOpen = false } }
                EButton { text: "Clear data"; onClicked: { panel = "confirm"; menuOpen = false } }
            }
        }
    }
    Rectangle {
        id: menuBottom
        visible: menuOpen && view === "page"
        anchors.left: parent.left; anchors.right: parent.right; anchors.bottom: parent.bottom
        height: 120
        color: "white"
        Rectangle { anchors.top: parent.top; width: parent.width; height: 3; color: "black" }
        MouseArea { anchors.fill: parent }
        RowLayout {
            anchors.fill: parent
            anchors.margins: 24
            spacing: 16
            EButton { text: "‹"; fontSize: 40; enabled: pageIndex > 0; onClicked: prevPage() }
            Slider {
                id: pageSlider
                Layout.fillWidth: true
                from: 0; to: Math.max(1, pageCount - 1); stepSize: 1
                snapMode: Slider.SnapAlways
                enabled: pageCount > 1
                value: pageIndex
                onMoved: goToPage(Math.round(value))
                background: Rectangle {
                    x: pageSlider.leftPadding
                    y: pageSlider.topPadding + pageSlider.availableHeight / 2 - height / 2
                    width: pageSlider.availableWidth; height: 6
                    color: "black"
                }
                handle: Rectangle {   // big enough for a finger
                    x: pageSlider.leftPadding + pageSlider.visualPosition * (pageSlider.availableWidth - width)
                    y: pageSlider.topPadding + pageSlider.availableHeight / 2 - height / 2
                    width: 44; height: 64; radius: 8
                    color: "white"; border.color: "black"; border.width: 4
                }
            }
            Text { text: "Page " + (pageIndex + 1) + " of " + pageCount; font.pixelSize: 26 }
            EButton { text: "›"; fontSize: 40; enabled: pageIndex < pageCount - 1; onClicked: nextPage() }
        }
    }

    // ------------------------------------------------------------ address bar panel
    Rectangle {
        visible: panel === "entry"
        anchors.fill: parent
        color: "white"
        MouseArea { anchors.fill: parent }
        ColumnLayout {
            anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top
            anchors.margins: 32
            spacing: 14
            RowLayout {
                Layout.fillWidth: true
                spacing: 12
                TextField {
                    id: entryField
                    Layout.fillWidth: true
                    implicitHeight: 80
                    font.pixelSize: 30
                    placeholderText: "Search DuckDuckGo or type an address"
                    inputMethodHints: Qt.ImhNoAutoUppercase | Qt.ImhNoPredictiveText | Qt.ImhUrlCharactersOnly
                    background: Rectangle { border.color: "black"; border.width: 3; radius: 10 }
                    onTextEdited: { updateEntryModel(); suggestTimer.restart() }
                    onAccepted: openInput(text)
                }
                EButton { text: "×"; fontSize: 40; visible: entryField.text !== ""; onClicked: { entryField.text = ""; updateEntryModel(); entryField.forceActiveFocus() } }
                EButton { text: "Go"; active: true; enabled: entryField.text.trim() !== ""; onClicked: openInput(entryField.text) }
                EButton { text: "Cancel"; onClicked: closePanel() }
            }
            Repeater {
                model: entryModel
                delegate: Item {
                    Layout.fillWidth: true
                    implicitHeight: 84
                    Column {
                        anchors.left: parent.left; anchors.right: parent.right
                        anchors.verticalCenter: parent.verticalCenter
                        spacing: 2
                        Text {
                            width: parent.width
                            text: modelData.kind === "suggest" ? modelData.label : modelData.label
                            font.pixelSize: 29
                            font.bold: modelData.kind === "suggest"
                            elide: Text.ElideRight
                        }
                        Text {
                            width: parent.width
                            visible: modelData.sub !== ""
                            text: modelData.sub
                            font.pixelSize: 21; color: "#555"; elide: Text.ElideRight
                        }
                    }
                    Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: "#999" }
                    MouseArea { anchors.fill: parent; onClicked: openInput(modelData.value) }
                }
            }
        }
    }

    // ------------------------------------------------------------ contents panel
    // Like a book's table of contents: the article's headings with their
    // page, and the site's navigation menu.
    Rectangle {
        visible: panel === "contents"
        anchors.fill: parent
        color: "white"
        MouseArea { anchors.fill: parent }
        ColumnLayout {
            anchors.fill: parent
            anchors.margins: 40
            spacing: 18
            RowLayout {
                Layout.fillWidth: true
                spacing: 12
                Text { text: "Contents"; font.pixelSize: 42; font.bold: true; Layout.fillWidth: true }
                EButton { text: "×"; fontSize: 40; onClicked: closePanel() }
            }
            RowLayout {
                spacing: 12
                EButton { text: "This page"; active: contentsTab === "headings"; onClicked: contentsTab = "headings" }
                EButton {
                    text: "Site menu"
                    active: contentsTab === "links"
                    onClicked: contentsTab = "links"
                }
            }
            Text {
                Layout.fillWidth: true
                text: pageData ? pageData.title : ""
                font.pixelSize: 24; color: "#444"; elide: Text.ElideRight
            }
            PagedList {
                id: tocList
                visible: contentsTab === "headings"
                Layout.fillWidth: true
                Layout.fillHeight: true
                rowHeight: 84
                items: {
                    const out = [{ text: "Beginning", level: 1, page: 0, n: -1 }]
                    const hs = pageData ? pageData.headings : []
                    for (let i = 0; i < hs.length; i++)
                        out.push({ text: hs[i].text, level: hs[i].level, page: headingPages[i] === undefined ? -1 : headingPages[i], n: i })
                    return out
                }
                readonly property int current: {
                    let c = 0
                    for (let i = 0; i < items.length; i++) if (items[i].page >= 0 && items[i].page <= pageIndex) c = i
                    return c
                }
                onVisibleChanged: if (visible) page = Math.floor(current / perPage)
                rowDelegate: Item {
                    width: tocList.width
                    height: tocList.rowHeight
                    function tapped() { if (modelData.page >= 0) { goToPage(modelData.page); closePanel() } }
                    Text {
                        anchors.left: parent.left
                        anchors.leftMargin: Math.max(0, modelData.level - 2) * 36
                        anchors.right: pageNo.left; anchors.rightMargin: 20
                        anchors.verticalCenter: parent.verticalCenter
                        text: modelData.text
                        font.pixelSize: modelData.level <= 2 ? 29 : 26
                        font.bold: tocList.items.indexOf(modelData) === tocList.current
                        color: modelData.page >= 0 ? "black" : "#888"
                        elide: Text.ElideRight
                    }
                    Text {
                        id: pageNo
                        anchors.right: parent.right
                        anchors.verticalCenter: parent.verticalCenter
                        text: modelData.page >= 0 ? String(modelData.page + 1) : ""
                        font.pixelSize: 26
                    }
                    Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: "#bbb" }
                }
            }
            PagedList {
                id: linkList
                visible: contentsTab === "links"
                Layout.fillWidth: true
                Layout.fillHeight: true
                rowHeight: 96
                items: pageData ? pageData.links : []
                emptyText: "This site has no navigation menu."
                rowDelegate: Item {
                    width: linkList.width
                    height: linkList.rowHeight
                    function tapped() { closePanel(); openLink(modelData.url) }
                    Column {
                        anchors.left: parent.left; anchors.right: parent.right
                        anchors.verticalCenter: parent.verticalCenter
                        spacing: 2
                        Text { width: parent.width; text: modelData.text; font.pixelSize: 28; elide: Text.ElideRight }
                        Text { width: parent.width; text: modelData.url.replace(/^https?:\/\/(www\.)?/, ""); font.pixelSize: 20; color: "#555"; elide: Text.ElideRight }
                    }
                    Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: "#bbb" }
                }
            }
        }
    }

    // ------------------------------------------------------------ settings panel
    Rectangle {
        visible: panel === "settings"
        anchors.fill: parent
        color: "white"
        MouseArea { anchors.fill: parent }
        ColumnLayout {
            anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top
            anchors.margins: 40
            spacing: 22
            RowLayout {
                Layout.fillWidth: true
                Text { text: "Settings"; font.pixelSize: 42; font.bold: true; Layout.fillWidth: true }
                EButton { text: "×"; fontSize: 40; onClicked: closePanel() }
            }
            Text { text: "Text size"; font.pixelSize: 30; font.bold: true }
            RowLayout {
                spacing: 16
                EButton { text: "A-"; enabled: (settings.fontSize || 32) > 22; onClicked: setFont((settings.fontSize || 32) - 4, !!settings.serif) }
                Text { text: (settings.fontSize || 32) + " px"; font.pixelSize: 28; Layout.preferredWidth: 110; horizontalAlignment: Text.AlignHCenter }
                EButton { text: "A+"; enabled: (settings.fontSize || 32) < 56; onClicked: setFont((settings.fontSize || 32) + 4, !!settings.serif) }
            }
            Text { text: "Font"; font.pixelSize: 30; font.bold: true }
            RowLayout {
                spacing: 16
                EButton { text: "Sans (Noto)"; active: !settings.serif; onClicked: setFont(settings.fontSize || 32, false) }
                EButton { text: "Serif (Garamond)"; active: !!settings.serif; onClicked: setFont(settings.fontSize || 32, true) }
            }
            Text { text: "Images"; font.pixelSize: 30; font.bold: true }
            RowLayout {
                spacing: 16
                EButton { text: "Show"; active: !settings.noImages; onClicked: send(reqSettings, { noImages: false }) }
                EButton { text: "Hide"; active: !!settings.noImages; onClicked: send(reqSettings, { noImages: true }) }
                EButton { text: "Colour"; visible: !settings.noImages; active: !settings.greyImages; onClicked: send(reqSettings, { greyImages: false }) }
                EButton { text: "Grey"; visible: !settings.noImages; active: !!settings.greyImages; onClicked: send(reqSettings, { greyImages: true }) }
            }
            Text { text: "Safe search (DuckDuckGo)"; font.pixelSize: 30; font.bold: true }
            RowLayout {
                spacing: 16
                EButton { text: "Strict"; active: settings.safeSearch === "strict"; onClicked: send(reqSettings, { safeSearch: "strict" }) }
                EButton { text: "Moderate"; active: settings.safeSearch === "moderate"; onClicked: send(reqSettings, { safeSearch: "moderate" }) }
                EButton { text: "Off"; active: settings.safeSearch === "off"; onClicked: send(reqSettings, { safeSearch: "off" }) }
            }
            Text { text: "Insecure pages (http://)"; font.pixelSize: 30; font.bold: true }
            RowLayout {
                spacing: 16
                EButton { text: "Block"; active: !settings.allowHTTP; onClicked: send(reqSettings, { allowHTTP: false }) }
                EButton { text: "Allow"; active: !!settings.allowHTTP; onClicked: send(reqSettings, { allowHTTP: true }) }
            }
            Text { text: "Privacy"; font.pixelSize: 30; font.bold: true }
            EButton { text: "Clear browsing data"; onClicked: panel = "confirm" }
            Text {
                Layout.fillWidth: true
                wrapMode: Text.Wrap
                font.pixelSize: 22; color: "#444"
                text: "Cookies are kept in memory only, never on the tablet, and forgotten when you close the browser. "
                      + "Tracking pixels and ads are never downloaded. No JavaScript is ever run. "
                      + "Pages are loaded by a sandboxed helper ("
                      + (appState.sandbox || "starting") + ") that cannot start programs, see your notebooks or reach your local network."
            }
            Text { text: "rm-browser " + (appState.version || ""); font.pixelSize: 22; color: "#444" }
        }
    }

    // ------------------------------------------------------------ confirm clearing
    Rectangle {
        visible: panel === "confirm"
        anchors.fill: parent
        color: "#80ffffff"
        MouseArea { anchors.fill: parent; onClicked: panel = "" }
        Rectangle {
            anchors.centerIn: parent
            width: Math.min(parent.width - 80, 800)
            height: confirmCol.implicitHeight + 64
            color: "white"
            border.color: "black"; border.width: 4; radius: 12
            MouseArea { anchors.fill: parent }
            ColumnLayout {
                id: confirmCol
                anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top
                anchors.margins: 32
                spacing: 24
                Text { text: "Clear browsing data?"; font.pixelSize: 36; font.bold: true }
                Text {
                    Layout.fillWidth: true
                    wrapMode: Text.Wrap
                    font.pixelSize: 26
                    text: "Deletes your history and searches, the back/forward list, cookies, and cached pages and images. Your settings are kept."
                }
                RowLayout {
                    spacing: 16
                    EButton { text: "Clear everything"; active: true; onClicked: { panel = ""; send(reqClearData) } }
                    EButton { text: "Cancel"; onClicked: panel = "" }
                }
            }
        }
    }

    // ------------------------------------------------------------ busy and messages
    Rectangle {
        visible: busyText !== ""
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.bottom: parent.bottom; anchors.bottomMargin: 140
        width: Math.min(parent.width - 80, busyRow.implicitWidth + 48)
        height: busyRow.implicitHeight + 32
        color: "white"
        border.color: "black"; border.width: 3; radius: 10
        RowLayout {
            id: busyRow
            anchors.centerIn: parent
            width: parent.width - 48
            spacing: 20
            Text { text: busyText; font.pixelSize: 26; elide: Text.ElideRight; Layout.fillWidth: true }
            EButton { text: "Stop"; onClicked: send(reqCancel) }
        }
    }
    Rectangle {
        visible: toast !== "" && busyText === ""
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.bottom: parent.bottom; anchors.bottomMargin: 140
        width: Math.min(parent.width - 80, toastText.implicitWidth + 48)
        height: toastText.implicitHeight + 32
        color: "black"; radius: 10
        Text {
            id: toastText
            anchors.centerIn: parent
            width: Math.min(implicitWidth, root.width - 128)
            text: toast; color: "white"; font.pixelSize: 26; wrapMode: Text.Wrap
        }
        MouseArea { anchors.fill: parent; onClicked: toast = "" }
    }

    // ------------------------------------------------------------ PDF export message
    Rectangle {
        visible: exportStatus !== ""
        anchors.fill: parent
        color: "white"
        MouseArea { anchors.fill: parent }   // nothing underneath reacts while saving
        ColumnLayout {
            anchors.centerIn: parent
            width: parent.width - 120
            spacing: 40
            Text {
                Layout.fillWidth: true
                text: exportStatus
                wrapMode: Text.Wrap
                horizontalAlignment: Text.AlignHCenter
                font.pixelSize: 32
            }
            EButton {
                Layout.alignment: Qt.AlignHCenter
                visible: !exporting
                text: "OK"
                onClicked: exportStatus = ""
            }
            EButton {
                Layout.alignment: Qt.AlignHCenter
                visible: exporting
                text: "Cancel"
                onClicked: { exporting = false; exportStatus = ""; send(reqExported) }
            }
        }
    }
}
