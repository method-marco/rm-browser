// rm-browser frontend (AppLoad / Qt Quick). A distraction-free, reader-only
// browser for e-ink: one tab, every page in reader view, split into pages
// that you turn instead of scrolling. Black on white, no animations, large
// touch targets.
import QtQuick 2.15
import QtQuick.Controls 2.15
import QtQuick.Layouts 1.15
import net.asivery.AppLoad 1.0
import "../shared"
import "../shared/fonts.js" as Fonts

Rectangle {
    id: root
    anchors.fill: parent
    color: Theme.paper

    // Every tap and swipe ends here at the latest. AppLoad shows the app on
    // top of xochitl's own screens (its list of apps): a tap that nothing
    // takes (empty space, a page still loading) would fall through to them
    // and open another app.
    MouseArea {
        anchors.fill: parent
        acceptedButtons: Qt.AllButtons
        onWheel: (w) => { w.accepted = true }
    }
    // The same for keys (AppLoad issue #41): a key the focused item leaves
    // alone goes on to xochitl. Text fields pass Enter on even after acting
    // on it, so typing an address and pressing Enter also opened whatever
    // was selected in the library behind the browser. Everything stops here.
    Keys.onPressed: (e) => { e.accepted = true }
    Keys.onReleased: (e) => { e.accepted = true }

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
        reader.stopTimers()
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

    property var appState: ({ settings: { fontSize: 32, font: "sans", lineSpacing: 125, align: "left", margin: "normal",
                                          safeSearch: "moderate", noImages: false, greyImages: false, allowHTTP: false },
                              history: [], canBack: false, canForward: false, sandbox: "", version: "" })
    readonly property var settings: appState.settings || {}
    // colours (app/shared/Theme.qml)
    Binding { target: Theme; property: "theme"; value: settings.theme || "default" }

    // ---- fonts and layout (the Font panel, app/shared) ----
    readonly property var fontChoices: Fonts.choices
    function familyOf(key) { return Fonts.familyOf(key) }
    readonly property int fontSize: reader.fontSize
    readonly property bool justified: reader.justified
    readonly property alias sideMargin: reader.sideMargin
    readonly property alias spread: reader.spread
    function setSetting(obj) {
        reader.recordTop()
        send(reqSettings, obj)
    }
    property string view: "home"        // home | page | search | error
    property bool menuOpen: false
    property string panel: ""           // "" | entry | contents | settings | font | confirm
    property string busyText: ""
    property string errorText: ""
    property bool errorRetry: false
    property string toast: ""

    // reader page (the pages themselves: ReaderView, app/shared)
    property var pageData: null
    property var headingPages: []       // page of each heading, -1 = not found
    readonly property alias pageStarts: reader.pageStarts
    readonly property alias pageHeights: reader.pageHeights
    readonly property alias pageIndex: reader.pageIndex
    readonly property alias pageCount: reader.pageCount
    readonly property alias topCharPos: reader.topCharPos
    readonly property var te: reader.textEdit
    readonly property var readerArea: reader.area

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
                reader.clear()
                menuOpen = false
                if (panel !== "settings" && panel !== "confirm") panel = ""
                break
            case 108: showToast(d.message); break
            case 109: pdfReady(d); break
            }
        }
    }

    Component.onCompleted: {
        fullScreen.start(root.parent)
        initTimer.start()
    }
    // Wait one moment so that the reader area has its size: the backend
    // sizes images to fit it.
    Timer {
        id: initTimer
        interval: 50
        onTriggered: send(reqInit, reader.viewport())
    }

    FullScreen { id: fullScreen }   // app/shared: no windowed mode

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
        return { name: exportName, url: pageData.url || "", fontSize: fontSize, font: settings.font || "sans",
                 justify: justified, areaWidth: Math.round(readerArea.width),
                 // two pages on screen: each PDF page is one of them with its margins
                 width: Math.round(spread ? readerArea.width + 2 * readerArea.x : root.width), height: Math.round(root.height),
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
    function showPage(p, position) {
        // the same page again at the page the reader is on (new margins or
        // images): stay exactly where the reader is
        const same = view === "page" && pageData && pageData.url === p.url && reader.pageHtml !== "" && position === pageIndex
        pageData = p
        headingPages = []
        view = "page"
        reader.setContent(pageHeader(p) + p.html, same ? { keep: true } : { page: position })
        menuOpen = false
        if (panel !== "settings") closePanel()
    }

    // title and meta line above the article (the backend's PDF builds the
    // same, see pageStream in export.go)
    function pageHeader(p) {
        let meta = []
        if (p.site) meta.push(esc(p.site))
        if (p.byline) meta.push(esc(p.byline))
        if (!p.secure) meta.push("<b>not secure (http)</b>")
        if (!p.reader) meta.push("simplified page")
        return "<h1>" + esc(p.title) + "</h1><p><small>" + meta.join(" · ") + "</small></p>"
    }
    function safeHtml(h) { return reader.safeHtml(h) }
    function pageOfY(y, starts) { return reader.pageOfY(y, starts) }
    function goToPage(i) { reader.goToPage(i) }
    function nextPage() { reader.nextPage() }
    function prevPage() { reader.prevPage() }

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
            res.push(reader.pageOfY(te.positionToRectangle(i).y))
        }
        headingPages = res
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

    // ------------------------------------------------------------ reader view
    // The article page by page (app/shared/ReaderView.qml); also measured
    // while hidden: the backend sizes images to fit it.
    ReaderView {
        id: reader
        anchors.fill: parent
        settings: root.settings
        imageDir: appState.imageDir || ""
        active: view === "page"
        interactive: view === "page" && !menuOpen && panel === ""
        onMenuRequested: menuOpen = true
        onLinkClicked: (href) => openLink(href)
        onPageTurned: (n) => send(reqPosition, { page: n })
        onLaidOut: computeHeadingPages()
        onViewportSettled: if (!initTimer.running) send(reqViewport, reader.viewport())
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
            Text { color: Theme.ink; text: "Browser"; font.pixelSize: 46; font.bold: true; Layout.fillWidth: true }
            EButton { text: "Settings"; onClicked: panel = "settings" }
            EButton { text: "Close"; onClicked: closeApp() }
        }
        Field { placeholder: "Search DuckDuckGo or type an address"; onClicked: openEntry() }
        RowLayout {
            Layout.fillWidth: true
            visible: !!appState.canForward
            EButton { text: "Forward ›"; onClicked: send(reqForward) }
        }
        Text { color: Theme.ink;
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
                    Text { color: Theme.ink;
                        width: parent.width
                        text: modelData.query ? "Search: " + modelData.query : (modelData.title || modelData.url)
                        font.pixelSize: 28; elide: Text.ElideRight
                    }
                    Text {
                        width: parent.width
                        text: modelData.query ? "DuckDuckGo" : hostOf(modelData.url)
                        font.pixelSize: 22; color: Theme.dim; elide: Text.ElideRight
                    }
                }
                Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: Theme.line }
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
                    Text { color: Theme.ink;
                        width: parent.width
                        text: modelData.title
                        font.pixelSize: 29; font.bold: true
                        wrapMode: Text.Wrap; maximumLineCount: 2; elide: Text.ElideRight
                    }
                    Text { width: parent.width; text: modelData.host || ""; font.pixelSize: 22; color: Theme.dim; elide: Text.ElideRight }
                    Text { color: Theme.ink;
                        width: parent.width
                        text: modelData.snippet
                        font.pixelSize: 23
                        wrapMode: Text.Wrap; maximumLineCount: 3; elide: Text.ElideRight
                    }
                }
                Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: Theme.line }
            }
        }
    }

    // ------------------------------------------------------------ error view
    ColumnLayout {
        visible: view === "error"
        anchors.centerIn: parent
        width: parent.width - 120
        spacing: 24
        Text { color: Theme.ink; text: "The page could not be loaded"; font.pixelSize: 36; font.bold: true; Layout.fillWidth: true; wrapMode: Text.Wrap }
        Text { color: Theme.ink; text: errorText; font.pixelSize: 28; Layout.fillWidth: true; wrapMode: Text.Wrap }
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
        color: Theme.paper
        Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 3; color: Theme.ink }
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
                EButton { text: "Aa  Font"; visible: view === "page"; onClicked: { menuOpen = false; panel = "font" } }
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
        color: Theme.paper
        Rectangle { anchors.top: parent.top; width: parent.width; height: 3; color: Theme.ink }
        MouseArea { anchors.fill: parent }
        RowLayout {
            anchors.fill: parent
            anchors.margins: 24
            spacing: 16
            EButton { text: "‹"; fontSize: 40; enabled: pageIndex > 0; onClicked: prevPage() }
            Slider {
                id: pageSlider
                Layout.fillWidth: true
                from: 0; to: Math.max(1, reader.spreadStart(pageCount - 1)); stepSize: reader.pageStep
                snapMode: Slider.SnapAlways
                enabled: pageCount > 1
                value: pageIndex
                onMoved: goToPage(Math.round(value))
                background: Rectangle {
                    x: pageSlider.leftPadding
                    y: pageSlider.topPadding + pageSlider.availableHeight / 2 - height / 2
                    width: pageSlider.availableWidth; height: 6
                    color: Theme.ink
                }
                handle: Rectangle {   // big enough for a finger
                    x: pageSlider.leftPadding + pageSlider.visualPosition * (pageSlider.availableWidth - width)
                    y: pageSlider.topPadding + pageSlider.availableHeight / 2 - height / 2
                    width: 44; height: 64; radius: 8
                    color: Theme.paper; border.color: Theme.ink; border.width: 4
                }
            }
            Text { color: Theme.ink; text: (spread && pageIndex + 1 < pageCount ? "Pages " : "Page ") + reader.pagesShown + " of " + pageCount; font.pixelSize: 26 }
            EButton { text: "›"; fontSize: 40; enabled: !reader.lastSpread; onClicked: nextPage() }
        }
    }

    // ------------------------------------------------------------ address bar panel
    Rectangle {
        visible: panel === "entry"
        anchors.fill: parent
        color: Theme.paper
        MouseArea { anchors.fill: parent }
        ColumnLayout {
            anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top
            anchors.margins: 32
            spacing: 14
            RowLayout {
                Layout.fillWidth: true
                spacing: 12
                TextField {
                    color: Theme.ink
                    placeholderTextColor: Theme.dim
                    id: entryField
                    Layout.fillWidth: true
                    implicitHeight: 80
                    font.pixelSize: 30
                    placeholderText: "Search DuckDuckGo or type an address"
                    inputMethodHints: Qt.ImhNoAutoUppercase | Qt.ImhNoPredictiveText | Qt.ImhUrlCharactersOnly
                    background: Rectangle { color: Theme.paper; border.color: Theme.ink; border.width: 3; radius: 10 }
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
                        Text { color: Theme.ink;
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
                            font.pixelSize: 21; color: Theme.dim; elide: Text.ElideRight
                        }
                    }
                    Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: Theme.line }
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
        color: Theme.paper
        MouseArea { anchors.fill: parent }
        ColumnLayout {
            anchors.fill: parent
            anchors.margins: 40
            spacing: 18
            RowLayout {
                Layout.fillWidth: true
                spacing: 12
                Text { color: Theme.ink; text: "Contents"; font.pixelSize: 42; font.bold: true; Layout.fillWidth: true }
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
                font.pixelSize: 24; color: Theme.dim; elide: Text.ElideRight
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
                    for (let i = 0; i < items.length; i++) if (items[i].page >= 0 && items[i].page < pageIndex + reader.pageStep) c = i
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
                        color: modelData.page >= 0 ? Theme.ink : Theme.dim
                        elide: Text.ElideRight
                    }
                    Text { color: Theme.ink;
                        id: pageNo
                        anchors.right: parent.right
                        anchors.verticalCenter: parent.verticalCenter
                        text: modelData.page >= 0 ? String(modelData.page + 1) : ""
                        font.pixelSize: 26
                    }
                    Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: Theme.line }
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
                        Text { color: Theme.ink; width: parent.width; text: modelData.text; font.pixelSize: 28; elide: Text.ElideRight }
                        Text { width: parent.width; text: modelData.url.replace(/^https?:\/\/(www\.)?/, ""); font.pixelSize: 20; color: Theme.dim; elide: Text.ElideRight }
                    }
                    Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: Theme.line }
                }
            }
        }
    }

    // ------------------------------------------------------------ font panel
    FontPanel {
        settings: root.settings
        open: panel === "font"
        onSetSetting: (obj) => root.setSetting(obj)
        onCloseRequested: closePanel()
    }

    // ------------------------------------------------------------ settings panel
    Rectangle {
        visible: panel === "settings"
        anchors.fill: parent
        color: Theme.paper
        MouseArea { anchors.fill: parent }
        ColumnLayout {
            anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top
            anchors.margins: 40
            spacing: 22
            RowLayout {
                Layout.fillWidth: true
                Text { color: Theme.ink; text: "Settings"; font.pixelSize: 42; font.bold: true; Layout.fillWidth: true }
                EButton { text: "×"; fontSize: 40; onClicked: closePanel() }
            }
            Text { color: Theme.ink; text: "Text"; font.pixelSize: 30; font.bold: true }
            EButton {
                text: "Aa  Font and layout: " + Fonts.find(settings.font).label + ", " + fontSize + " px"
                onClicked: panel = "font"
            }
            Text { color: Theme.ink; text: "Images"; font.pixelSize: 30; font.bold: true }
            RowLayout {
                spacing: 16
                EButton { text: "Show"; active: !settings.noImages; onClicked: send(reqSettings, { noImages: false }) }
                EButton { text: "Hide"; active: !!settings.noImages; onClicked: send(reqSettings, { noImages: true }) }
                EButton { text: "Colour"; visible: !settings.noImages; active: !settings.greyImages; onClicked: send(reqSettings, { greyImages: false }) }
                EButton { text: "Grey"; visible: !settings.noImages; active: !!settings.greyImages; onClicked: send(reqSettings, { greyImages: true }) }
            }
            Text { color: Theme.ink; text: "Safe search (DuckDuckGo)"; font.pixelSize: 30; font.bold: true }
            RowLayout {
                spacing: 16
                EButton { text: "Strict"; active: settings.safeSearch === "strict"; onClicked: send(reqSettings, { safeSearch: "strict" }) }
                EButton { text: "Moderate"; active: settings.safeSearch === "moderate"; onClicked: send(reqSettings, { safeSearch: "moderate" }) }
                EButton { text: "Off"; active: settings.safeSearch === "off"; onClicked: send(reqSettings, { safeSearch: "off" }) }
            }
            Text { color: Theme.ink; text: "Insecure pages (http://)"; font.pixelSize: 30; font.bold: true }
            RowLayout {
                spacing: 16
                EButton { text: "Block"; active: !settings.allowHTTP; onClicked: send(reqSettings, { allowHTTP: false }) }
                EButton { text: "Allow"; active: !!settings.allowHTTP; onClicked: send(reqSettings, { allowHTTP: true }) }
            }
            Text { color: Theme.ink; text: "Privacy"; font.pixelSize: 30; font.bold: true }
            EButton { text: "Clear browsing data"; onClicked: panel = "confirm" }
            Text {
                Layout.fillWidth: true
                wrapMode: Text.Wrap
                font.pixelSize: 22; color: Theme.dim
                text: "Cookies are kept in memory only, never on the tablet, and forgotten when you close the browser. "
                      + "Tracking pixels and ads are never downloaded. No JavaScript is ever run. "
                      + "Pages are loaded by a sandboxed helper ("
                      + (appState.sandbox || "starting") + ") that cannot start programs, see your notebooks or reach your local network."
            }
            Text { text: "rm-browser " + (appState.version || ""); font.pixelSize: 22; color: Theme.dim }
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
            color: Theme.paper
            border.color: Theme.ink; border.width: 4; radius: 12
            MouseArea { anchors.fill: parent }
            ColumnLayout {
                id: confirmCol
                anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top
                anchors.margins: 32
                spacing: 24
                Text { color: Theme.ink; text: "Clear browsing data?"; font.pixelSize: 36; font.bold: true }
                Text { color: Theme.ink;
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
        color: Theme.paper
        border.color: Theme.ink; border.width: 3; radius: 10
        RowLayout {
            id: busyRow
            anchors.centerIn: parent
            width: parent.width - 48
            spacing: 20
            Text { color: Theme.ink; text: busyText; font.pixelSize: 26; elide: Text.ElideRight; Layout.fillWidth: true }
            EButton { text: "Stop"; onClicked: send(reqCancel) }
        }
    }
    Rectangle {
        visible: toast !== "" && busyText === ""
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.bottom: parent.bottom; anchors.bottomMargin: 140
        width: Math.min(parent.width - 80, toastText.implicitWidth + 48)
        height: toastText.implicitHeight + 32
        color: Theme.ink; radius: 10
        Text {
            id: toastText
            anchors.centerIn: parent
            width: Math.min(implicitWidth, root.width - 128)
            text: toast; color: Theme.paper; font.pixelSize: 26; wrapMode: Text.Wrap
        }
        MouseArea { anchors.fill: parent; onClicked: toast = "" }
    }

    // ------------------------------------------------------------ PDF export message
    Rectangle {
        visible: exportStatus !== ""
        anchors.fill: parent
        color: Theme.paper
        MouseArea { anchors.fill: parent }   // nothing underneath reacts while saving
        ColumnLayout {
            anchors.centerIn: parent
            width: parent.width - 120
            spacing: 40
            Text { color: Theme.ink;
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
