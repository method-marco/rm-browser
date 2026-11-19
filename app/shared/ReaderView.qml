// The reader, shared by the browser and Books: rich text split into pages
// you turn instead of scrolling (no line or picture is cut at the bottom
// edge), two pages side by side like an open book when the tablet is
// turned sideways. Taps: left third back, right third forward, middle the
// menu; links win over page turns. Swipes turn pages too.
import QtQuick 2.15
import "fonts.js" as Fonts

Item {
    id: rv
    objectName: "readerView"

    // ---- set by the app
    property var settings: ({})          // fontSize, font, lineSpacing, align, margin, paragraphs
    property string imageDir: ""         // pictures only from here (see safeHtml)
    property bool active: false          // the reader is shown
    property bool interactive: true      // taps and swipes (no menu or panel open)
    property real imageShare: 0.8        // pictures may take this share of the page height
    property bool bookSpacing: false     // space around headings and paragraphs in text units (books)
    property string footer: pagesShown + " / " + pageCount
    signal menuRequested()
    signal linkClicked(string href)
    signal pageTurned(int page)          // the user moved to another page
    signal pastEnd()                     // next page after the last one
    signal beforeStart()                 // previous page before the first one
    signal laidOut()                     // pages computed (new text, reflow)
    signal viewportSettled()             // the page area has a new size (send it to the backend)

    visible: active

    // ---- typography
    readonly property int fontSize: settings.fontSize || 32
    readonly property int lineSpacing: settings.lineSpacing || 125
    readonly property bool justified: settings.align === "justify"
    readonly property bool indent: settings.paragraphs === "indent"
    readonly property int sideMargin: settings.margin === "narrow" ? 24 : settings.margin === "wide" ? 104 : 56
    readonly property string fontFamily: Fonts.familyOf(settings.font)

    // Tablet turned sideways (xochitl then gives the app a wide window): two
    // pages side by side like an open book, turned two at a time. The left
    // page is always an even one, so the spreads stay the same.
    readonly property bool spread: width > height
    readonly property int gutter: Math.max(48, sideMargin)
    readonly property int pageStep: spread ? 2 : 1

    // ---- pages
    property string source: ""           // the rich text as the app gave it
    property string pageHtml: ""
    property var pageStarts: [0]         // y of each screen page in the text
    property var pageHeights: [1]        // visible height of each page (no line is cut in half)
    property int pageIndex: 0
    property int pendingPosition: -1     // page to show once the text is laid out
    property int pendingChar: -1         // ... or the page with this visible character
    property bool pendingEnd: false      // ... or the last page
    property int topCharPos: -1          // first character on screen: kept when the text reflows
    readonly property int pageCount: pageStarts.length
    readonly property bool lastSpread: pageIndex + pageStep >= pageCount
    // "3 / 20", or "3–4 / 20" with two pages on screen
    readonly property string pagesShown: (pageIndex + 1) + (spread && pageIndex + 1 < pageCount ? "–" + (pageIndex + 2) : "")

    readonly property alias textEdit: te
    readonly property alias area: readerArea
    readonly property alias rightPage: rightPage

    // the typefaces that come with the apps
    Instantiator {
        model: Fonts.files()
        delegate: FontLoader { source: Qt.resolvedUrl("../fonts/" + modelData) }
    }

    // ------------------------------------------------------------ content
    // Defence in depth: the backends already write only safe rich text, but
    // xochitl (which shows it) runs as root, so check again here: images
    // only from the backend's image folder, nothing else Qt could load.
    function safeHtml(h) {
        const dir = imageDir ? "file://" + imageDir + "/" : ""
        return String(h || "")
            .replace(/<img\b[^>]*>/gi, function (tag) {
                const m = tag.match(/\bsrc="([^"]*)"/i)
                return dir !== "" && m && m[1].indexOf(dir) === 0 && m[1].indexOf("..") < 0 ? tag : ""
            })
            .replace(/\bbackground\s*=\s*"[^"]*"/gi, "")
            .replace(/url\s*\(/gi, "")
            .replace(/<(link|style|script|iframe|object|embed)\b[^>]*>/gi, "")
    }
    // Line spacing, alignment and paragraph style live in the rich text:
    // rewrite the paragraphs the backends wrote (style="line-height:125%").
    // Book style: first lines indented, no gap between paragraphs (only
    // plain paragraphs; centred ones and headings keep theirs).
    function restyle(h) {
        let out = String(h).replace(/line-height:125%/g, "line-height:" + lineSpacing + "%")
        // the book's own style (Books) is in em, which Qt's rich text
        // ignores: pixels for the text size, inside style attributes only
        if (hasEm) out = out.replace(/style="[^"]*"/g, function (st) {
            return st.replace(/(\d*\.?\d+)em\b/g, function (m, v) { return Math.round(parseFloat(v) * fontSize) + "px" })
        })
        if (indent)
            out = out.replace(/<p style="line-height:(\d+)%">/g, '<p style="line-height:$1%; text-indent:' + Math.round(fontSize * 1.4)
                              + 'px; margin-top:0px; margin-bottom:0px">')
        if (bookSpacing) out = spaceBlocks(out)
        // links are written black: the theme's text colour
        if (String(Theme.ink) !== "#000000") out = out.replace(/color:#000000/g, "color:" + String(Theme.ink))
        if (justified) out = out.replace(/<(p|li) style="line-height/g, '<$1 align="justify" style="line-height')
        return out
    }
    // Space as in a printed book, in proportion to the text (Qt's own
    // margins are a few fixed pixels, too little for large text): headings
    // get room above and below unless the book sets its own, spaced
    // paragraphs a gap, and the first paragraph after a heading, a rule or
    // a quote no indent.
    function spaceBlocks(h) {
        const px = function (e) { return Math.round(e * fontSize) + "px" }
        const room = { 1: [2.0, 1.0], 2: [1.8, 1.0], 3: [1.5, 0.9] }
        let out = h.replace(/<h([1-6])((?: align="[a-z]+")?)(?: style="([^"]*)")?>/g, function (m, lv, al, st) {
            st = st || ""
            const r = room[lv] || [1.3, 0.8]
            let add = []
            if (st.indexOf("margin-top") < 0) add.push("margin-top:" + px(r[0]))
            if (st.indexOf("margin-bottom") < 0) add.push("margin-bottom:" + px(r[1]))
            const all = [st].concat(add).filter(function (x) { return x !== "" }).join("; ")
            return "<h" + lv + al + ' style="' + all + '">'
        })
        if (!indent)
            out = out.replace(/<p style="line-height:(\d+)%">/g, '<p style="line-height:$1%; margin-top:0px; margin-bottom:' + px(0.7) + '">')
        // (only the panel's own indent: one the book sets is the book's choice)
        const own = "text-indent:" + Math.round(fontSize * 1.4) + "px; margin-top:0px; margin-bottom:0px\""
        const after = /(<\/h[1-6]>|<hr\/>|<\/blockquote>|<\/table>)(<p(?: align="[a-z]+")? style="line-height:\d+%; )(text-indent:\d+px; margin-top:0px; margin-bottom:0px")/g
        out = out.replace(after, function (m, end, open, rest) {
            return rest === own ? end + open + "text-indent:0px; margin-top:0px; margin-bottom:0px\"" : m
        })
        return out
    }
    // target: {page: n} | {char: k} (visible character) | {end: true} |
    // {keep: true} (the same document again: stay where the reader is)
    function setContent(html, target) {
        target = target || {}
        let keep = -1
        if (target.keep && pageHtml !== "") { recordTop(); keep = topCharPos }
        source = html
        pendingPosition = target.page !== undefined ? target.page : -1
        pendingChar = target.char !== undefined && target.char >= 0 ? target.char : -1
        pendingEnd = !!target.end
        topCharPos = keep
        pageIndex = 0
        pageStarts = [0]
        pageHeights = [readerArea.height]
        // Qt's TextEdit doesn't redraw correctly when its rich text is
        // replaced in place (parts of the new page stay blank, old images
        // linger). Clearing it first and setting the new text a moment
        // later is reliable.
        pageHtml = ""
        nextHtml = restyle(safeHtml(html))
        setTextTimer.restart()
        schedulePaginate()
    }
    function clear() {
        source = ""
        pageHtml = ""
        nextHtml = ""
        pageStarts = [0]
        pageHeights = [1]
        pageIndex = 0
    }
    // the same text again with a new layout, keeping the reading position.
    // topCharPos is current (recorded at every pagination and page turn);
    // not recorded again here: the new font may already be laid out, and
    // the old page starts would point at other text.
    function rerender() {
        if (source === "") return
        const keep = topCharPos
        pageHtml = ""
        nextHtml = restyle(safeHtml(source))
        setTextTimer.restart()
        topCharPos = keep
        schedulePaginate()
    }
    readonly property bool hasEm: /style="[^"]*\dem\b/.test(source)
    property string layoutKey: lineSpacing + "|" + justified + "|" + indent + "|" + (indent || hasEm || bookSpacing ? fontSize : 0) + "|" + Theme.current
    onLayoutKeyChanged: if (active && source !== "") rerender()

    property string nextHtml: ""
    Timer {
        id: setTextTimer
        interval: 30
        onTriggered: { pageHtml = nextHtml; nextHtml = ""; schedulePaginate() }
    }
    Timer { id: paginateTimer; interval: 60; onTriggered: paginate() }
    function schedulePaginate() { if (active) paginateTimer.restart() }
    onActiveChanged: if (active) schedulePaginate()
    function stopTimers() { setTextTimer.stop(); paginateTimer.stop(); viewportTimer.stop() }

    // Splits the laid-out text into screen pages. A page ends where the
    // next one starts; a line (or image) that would be cut by the bottom
    // edge moves to the next page instead.
    function paginate() {
        if (!active || pageHtml === "" || readerArea.height < 100) return
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
        if (pendingPosition >= 0) target = pendingPosition
        else if (pendingEnd) target = starts.length - 1
        else if (pendingChar >= 0) target = pageOfY(te.positionToRectangle(positionOfChar(pendingChar)).y, starts)
        else if (topCharPos >= 0) target = pageOfY(te.positionToRectangle(topCharPos).y, starts)
        pendingPosition = -1; pendingChar = -1; pendingEnd = false
        pageStarts = starts
        pageHeights = heights
        pageIndex = spreadStart(Math.max(0, Math.min(target, starts.length - 1)))
        recordTop()
        laidOut()
    }
    function pageOfY(y, starts) {
        const s = starts || pageStarts
        let i = 0
        while (i + 1 < s.length && s[i + 1] <= y + 1) i++
        return i
    }
    // (not while the text is being replaced: an empty text would say 0)
    function recordTop() {
        if (pageHtml === "" || te.length === 0) return
        topCharPos = te.positionAt(4, pageStarts[pageIndex] + 2)
    }
    function spreadStart(i) { return spread ? i - i % 2 : i }
    function goToPage(i) {
        const n = spreadStart(Math.max(0, Math.min(i, pageCount - 1)))
        if (n === pageIndex) return
        pageIndex = n
        recordTop()
        pageTurned(n)
    }
    function nextPage() { if (lastSpread) pastEnd(); else goToPage(pageIndex + pageStep) }
    function prevPage() { if (pageIndex === 0) beforeStart(); else goToPage(pageIndex - pageStep) }

    // ------------------------------------------------------------ positions
    // Positions that survive any layout: the n-th visible character (not
    // whitespace, not a picture or table mark, not an invisible format
    // character). The Books backend counts the same way (isCounted in
    // backend/books/render.go). Counted once per text, by UTF-16 unit:
    // a character outside the BMP counts at its first half.
    function countedCode(c) {
        if (c <= 32) return c !== 9 && c !== 10 && c !== 11 && c !== 12 && c !== 13 && c !== 32
        if (c === 0xA0 || c === 0xAD || c === 0x1680 || c === 0x2028 || c === 0x2029 || c === 0x202F || c === 0x205F
            || c === 0x3000 || c === 0xFEFF || c === 0xFFFC || c === 0x2060) return false
        if (c >= 0x2000 && c <= 0x200D) return false
        if (c >= 0xFDD0 && c <= 0xFDD2) return false
        if (c >= 0xDC00 && c <= 0xDFFF) return false
        return true
    }
    property var counts: null
    function countsOf() {
        if (counts) return counts
        const len = te.length
        const all = te.getText(0, len)
        if (all.length !== len) { counts = { exact: false }; return counts }   // tables (see exportLayout)
        const c = new Int32Array(len + 1)
        let n = 0
        for (let i = 0; i < len; i++) { c[i] = n; if (countedCode(all.charCodeAt(i))) n++ }
        c[len] = n
        counts = { exact: true, c: c }
        return counts
    }
    function countIn(s) {
        let n = 0
        for (let i = 0; i < s.length; i++) if (countedCode(s.charCodeAt(i))) n++
        return n
    }
    // visible characters before position pos
    function charIndexAt(pos) {
        pos = Math.max(0, Math.min(pos, te.length))
        const k = countsOf()
        return k.exact ? k.c[pos] : countIn(te.getText(0, pos))
    }
    // the position of the n-th visible character (0-based)
    function positionOfChar(n) {
        const k = countsOf()
        let lo = 0, hi = te.length
        while (lo < hi) {
            const mid = (lo + hi) >> 1
            if ((k.exact ? k.c[mid + 1] : countIn(te.getText(0, mid + 1))) <= n) lo = mid + 1
            else hi = mid
        }
        return lo
    }
    // the reading position: the first visible character on screen
    function topChar() { return charIndexAt(topCharPos >= 0 ? topCharPos : 0) }

    // The link under a finger: also look a little around the touch point,
    // fingers are not as exact as a mouse. With two pages on screen, the
    // right half belongs to the right page.
    function linkAt(item, x, y) {
        const right = spread && rightPage.item && item.mapToItem(rv, x, y).x > readerArea.x + readerArea.width + gutter / 2
        const t = right ? rightPage.item.textEdit : te
        const i = pageIndex + (right ? 1 : 0)
        if (i >= pageCount) return ""
        const pt = item.mapToItem(t, x, y)
        const top = pageStarts[i], bottom = top + pageHeights[i]
        const offsets = [[0, 0], [0, -14], [0, 14], [-14, 0], [14, 0]]
        for (const o of offsets) {
            const ty = pt.y + o[1]
            if (ty < top || ty > bottom) continue
            const l = t.linkAt(pt.x + o[0], ty)
            if (l) return l
        }
        return ""
    }

    // the page area for the backend: pictures are sized to fit it
    function viewport() {
        return { width: Math.floor(readerArea.width), height: Math.floor(readerArea.height * imageShare) }
    }

    // ------------------------------------------------------------ the pages
    // Where the text is drawn (also measured while hidden: the backends
    // size pictures to fit it). In landscape this is the left page.
    Item {
        id: readerArea
        objectName: "readerArea"
        anchors.left: parent.left; anchors.top: parent.top; anchors.bottom: parent.bottom
        anchors.leftMargin: sideMargin
        anchors.topMargin: 48; anchors.bottomMargin: 76
        width: spread ? Math.floor((parent.width - 2 * sideMargin - gutter) / 2) : parent.width - 2 * sideMargin
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
                color: Theme.ink
                font.pixelSize: rv.fontSize
                font.family: rv.fontFamily
                text: pageHtml
                onTextChanged: rv.counts = null
                onContentHeightChanged: { rv.counts = null; schedulePaginate() }
                onWidthChanged: schedulePaginate()
                onFontChanged: { rv.counts = null; schedulePaginate() }
            }
        }
    }
    Timer {
        id: viewportTimer
        interval: 500
        onTriggered: rv.viewportSettled()
    }

    // The right page of a spread: a second copy of the text with the same
    // width and font (so the same layout), showing the next page. Only made
    // in landscape.
    Rectangle {   // fold between the pages
        visible: spread && pageHtml !== ""
        x: readerArea.x + readerArea.width + gutter / 2
        y: readerArea.y; width: 1; height: readerArea.height
        color: Theme.line
    }
    Loader {
        id: rightPage
        objectName: "rightPage"
        active: spread
        x: readerArea.x + readerArea.width + gutter
        y: readerArea.y
        // no size here: a Loader with a size forces it on the page, and the
        // page's height must end above a line that doesn't fit
        sourceComponent: Flickable {
            readonly property alias textEdit: te2
            readonly property int index: pageIndex + 1
            visible: index < pageCount
            width: readerArea.width
            height: pageHeights[index] || 0
            clip: true
            interactive: false
            contentWidth: width
            contentHeight: te2.contentHeight
            contentY: pageStarts[index] || 0
            TextEdit {
                id: te2
                objectName: "te2"
                width: readerArea.width
                readOnly: true
                selectByMouse: false
                activeFocusOnPress: false
                textFormat: TextEdit.RichText
                wrapMode: TextEdit.Wrap
                color: Theme.ink
                font.pixelSize: rv.fontSize
                font.family: rv.fontFamily
                text: pageHtml
            }
        }
    }

    // taps and swipes on the page: left third back, right third forward,
    // middle opens the menu; links win over page turns
    MouseArea {
        anchors.fill: parent
        visible: rv.interactive
        property real px: 0
        property real py: 0
        onPressed: (m) => { px = m.x; py = m.y }
        onReleased: (m) => {
            const dx = m.x - px, dy = m.y - py
            if (Math.abs(dx) > 80 && Math.abs(dx) > Math.abs(dy)) { if (dx < 0) nextPage(); else prevPage(); return }
            if (Math.abs(dx) > 30 || Math.abs(dy) > 30) return
            const link = linkAt(this, m.x, m.y)
            if (link) { linkClicked(link); return }
            if (m.x < width / 3) prevPage()
            else if (m.x > width * 2 / 3) nextPage()
            else menuRequested()
        }
    }

    Text {   // page number
        objectName: "pageFooter"
        anchors.bottom: parent.bottom; anchors.bottomMargin: 22
        anchors.horizontalCenter: parent.horizontalCenter
        width: Math.min(implicitWidth, parent.width - 2 * sideMargin)
        elide: Text.ElideMiddle
        text: rv.footer
        font.pixelSize: 22; color: Theme.dim
    }
}
