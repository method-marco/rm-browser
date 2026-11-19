// rm-books frontend (AppLoad / Qt Quick): an EPUB reader for e-ink. The
// books are the EPUBs in the library folder "Library"; each chapter is
// shown page by page with the browser's reader (app/shared/ReaderView.qml)
// and font panel. The reading position syncs with KOReader's sync server
// (kosync). Black on white, no animations, large touch targets.
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

    // Every tap ends here at the latest: AppLoad shows the app on top of
    // xochitl's own screens, and a tap that nothing takes would open
    // whatever is behind it (see the browser).
    MouseArea {
        anchors.fill: parent
        acceptedButtons: Qt.AllButtons
        onWheel: (w) => { w.accepted = true }
    }
    // the same for keys (AppLoad issue #41)
    Keys.onPressed: (e) => { e.accepted = true }
    Keys.onReleased: (e) => { e.accepted = true }

    // Required by AppLoad.
    signal close
    // Called by AppLoad when the app closes (maybe twice). Don't terminate
    // the backend here (that crashes xochitl, see the browser).
    function unloading() {
        cleanUpForClose()
        try { send(reqClosed, {}) } catch (e) {}   // sends the reading position
    }
    function cleanUpForClose() {
        reader.stopTimers()
        toastTimer.stop()
        initTimer.stop()
        Qt.inputMethod.hide()
        root.forceActiveFocus()
    }
    function closeApp() {
        cleanUpForClose()
        send(reqClosed, {})
        closeTimer.start()   // let the keyboard detach before AppLoad tears the app down
    }
    Timer { id: closeTimer; interval: 200; onTriggered: root.close() }

    // ---- protocol (must match backend/books/main.go) ----
    readonly property int reqInit: 1
    readonly property int reqOpen: 2
    readonly property int reqChapter: 3
    readonly property int reqPosition: 4
    readonly property int reqLink: 5
    readonly property int reqSettings: 6
    readonly property int reqLibrary: 7
    readonly property int reqSync: 8
    readonly property int reqLogin: 9
    readonly property int reqLogout: 10
    readonly property int reqViewport: 11
    readonly property int reqClosed: 12
    readonly property int reqPull: 13
    readonly property int reqPush: 14

    property var appState: ({ settings: { fontSize: 34, font: "literata", lineSpacing: 125, align: "justify", margin: "normal",
                                          paragraphs: "indent", noImages: false, greyImages: false },
                              sync: {}, defaults: null, version: "", sandbox: "", folder: "Library" })
    readonly property var settings: appState.settings || {}
    // colours (app/shared/Theme.qml)
    Binding { target: Theme; property: "theme"; value: settings.theme || "default" }
    readonly property var syncInfo: appState.sync || {}

    property string view: "library"     // library | book | error
    property bool menuOpen: false
    property string panel: ""           // "" | contents | font | settings | login
    property string busyText: ""
    property string errorText: ""
    property string toast: ""

    // library
    property var books: []
    property bool libraryFound: true
    property string libraryError: ""

    // the open book
    property var book: null             // {id, title, author, chapters, counts, toc, imageBook}
    property int chapter: 0
    property int topChar: 0             // reading position: first visible character on screen
    property bool moved: false          // the reader turned a page since the book was opened
    property bool reportOnLayout: false // tell the backend the position once the new chapter is laid out
    property var backStack: []          // positions before following links (‹ Back)
    readonly property var prefix: {
        const out = []
        let n = 0
        for (const c of (book ? book.counts : [])) { out.push(n); n += c }
        return out
    }
    readonly property int totalChars: {
        let n = 0
        for (const c of (book ? book.counts : [])) n += c
        return n
    }
    function percentAt(ch, k) {
        if (!book || book.chapters === 0) return 0
        const cnt = book.counts[ch] || 0
        if (book.imageBook) return (ch + (cnt > 0 ? Math.min(k, cnt) / cnt : 0)) / book.chapters
        return totalChars > 0 ? (prefix[ch] + Math.min(k, cnt)) / totalChars : 0
    }
    readonly property int percent: Math.round(100 * percentAt(chapter, topChar))
    // the chapter title: the last entry of the table of contents at or before the position
    readonly property string chapterTitle: {
        if (!book) return ""
        let t = ""
        for (const e of book.toc) if (e.chapter < chapter || (e.chapter === chapter && e.char <= topChar)) t = e.title
        return t
    }

    function send(type, obj) { backend.sendMessage(type, JSON.stringify(obj || {})) }

    AppLoad {
        id: backend
        applicationID: "rm-books"
        onMessageReceived: (type, contents) => {
            let d = {}
            try { d = contents ? JSON.parse(contents) : {} } catch (e) { return }
            switch (type) {
            case 101: root.appState = d; break
            case 102:
                busyText = ""
                errorText = d.message
                if (view === "book" && book) showToast(d.message)
                else { view = "error"; menuOpen = false }
                break
            case 103: showChapter(d); break
            case 104:
                book = d
                chapter = 0
                topChar = 0
                moved = false
                backStack = []
                break
            case 105:
                books = d.books || []
                libraryFound = !!d.found
                libraryError = d.error || ""
                view = "library"
                book = null
                reader.clear()
                menuOpen = false
                if (panel !== "settings" && panel !== "login") panel = ""
                break
            case 106: busyText = d.busy ? d.text : ""; break
            case 107: showToast(d.message); break
            case 108: syncedPosition(d); break
            }
        }
    }

    Component.onCompleted: {
        fullScreen.start(root.parent)
        initTimer.start()
    }
    // wait one moment so that the reader area has its size: pictures are sized to fit it
    Timer {
        id: initTimer
        interval: 50
        onTriggered: send(reqInit, reader.viewport())
    }
    FullScreen { id: fullScreen }

    Timer { id: toastTimer; interval: 4000; onTriggered: toast = "" }
    function showToast(t) { toast = t; toastTimer.restart() }

    // ------------------------------------------------------------ reading
    function openBook(id) {
        menuOpen = false
        send(reqOpen, { id: id })
    }
    function showChapter(d) {
        if (!book || d.book !== book.id) return
        chapter = d.chapter
        view = "book"
        reader.setContent(d.html, d.end ? { end: true } : { char: Math.max(0, d.char) })
        busyText = ""
    }
    // a chapter the reader asked for (next/previous, contents, link, sync):
    // its position is the new reading position once it is laid out
    function goTo(ch, k, dir) {
        reportOnLayout = true
        moved = true
        send(reqChapter, { chapter: ch, char: k, dir: dir || 1 })
    }
    function remember() {
        const s = backStack.slice()
        s.push({ chapter: chapter, char: topChar })
        backStack = s.slice(-20)
    }
    function goBack() {
        if (backStack.length === 0) return
        const s = backStack.slice()
        const p = s.pop()
        backStack = s
        menuOpen = false
        goTo(p.chapter, p.char, 1)
    }
    function reportPosition() {
        topChar = reader.topChar()
        send(reqPosition, { chapter: chapter, char: topChar })
    }
    function syncedPosition(d) {
        const pct = Math.round(d.percent * 100) + "%"
        if (moved && !d.manual) {
            showToast(d.device + " is at " + pct + ". Menu › Sync goes there.")
            return
        }
        goTo(d.chapter, d.char, 1)
        showToast("Synced: " + pct + ", from " + d.device + ".")
    }
    function openLink(href) {
        if (href.indexOf("book:") === 0) {
            remember()
            reportOnLayout = true
            moved = true
            send(reqLink, { href: href })
        } else if (/^https?:\/\//i.test(href)) {
            showToast("A web link: " + href)
        }
    }
    function setSetting(obj) {
        reader.recordTop()
        send(reqSettings, obj)
    }
    function closePanel() {
        if (panel === "login") {
            Qt.inputMethod.hide()
            root.forceActiveFocus()
        }
        panel = ""
    }
    function toLibrary() {
        menuOpen = false
        closePanel()
        send(reqLibrary)
    }

    // ------------------------------------------------------------ library view
    ColumnLayout {
        visible: view === "library"
        anchors.fill: parent
        anchors.margins: 40
        spacing: 22

        RowLayout {
            Layout.fillWidth: true
            spacing: 12
            Text { color: Theme.ink; text: "Books"; font.pixelSize: 46; font.bold: true; Layout.fillWidth: true }
            EButton { text: "Settings"; onClicked: panel = "settings" }
            EButton { text: "Close"; onClicked: closeApp() }
        }
        Text {
            Layout.fillWidth: true
            text: libraryFound ? "Folder “" + appState.folder + "” · " + books.length + (books.length === 1 ? " book" : " books") : ""
            visible: text !== ""
            font.pixelSize: 24; color: Theme.dim
        }
        PagedList {
            id: libraryList
            Layout.fillWidth: true
            Layout.fillHeight: true
            items: books
            rowHeight: 112
            emptyText: libraryError !== "" ? libraryError
                       : !libraryFound ? "There is no folder “" + appState.folder + "” in your library. Make one (in the library or the reMarkable app) and put EPUB books in it."
                       : "No EPUB books in “" + appState.folder + "” yet. Add some with the reMarkable app or by e-mail; PDFs are not shown here."
            rowDelegate: Item {
                width: libraryList.width
                height: libraryList.rowHeight
                function tapped() { openBook(modelData.id) }
                Column {
                    anchors.left: parent.left; anchors.right: progressText.left; anchors.rightMargin: 20
                    anchors.verticalCenter: parent.verticalCenter
                    spacing: 4
                    Text { color: Theme.ink; width: parent.width; text: modelData.title; font.pixelSize: 29; elide: Text.ElideRight }
                    Text {
                        width: parent.width
                        text: [modelData.author || "", modelData.folder || ""].filter(function (x) { return x !== "" }).join(" · ")
                        visible: text !== ""
                        font.pixelSize: 22; color: Theme.dim; elide: Text.ElideRight
                    }
                }
                Text {
                    id: progressText
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    text: modelData.read > 0 ? Math.round(modelData.percent * 100) + "%" : "New"
                    font.pixelSize: 24; color: modelData.read > 0 ? Theme.ink : Theme.dim
                }
                Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: Theme.line }
            }
        }
    }

    // ------------------------------------------------------------ reader
    ReaderView {
        id: reader
        anchors.fill: parent
        settings: root.settings
        imageDir: appState.imageDir || ""
        imageShare: 0.9
        bookSpacing: true
        active: view === "book"
        interactive: view === "book" && !menuOpen && panel === ""
        footer: pagesShown + " / " + pageCount + "  ·  " + root.percent + "%"
        onMenuRequested: menuOpen = true
        onLinkClicked: (href) => root.openLink(href)
        onPageTurned: (n) => { root.moved = true; root.reportPosition() }
        onPastEnd: {
            if (!book) return
            if (chapter + 1 < book.chapters) root.goTo(chapter + 1, 0, 1)
            else showToast("The end of the book.")
        }
        onBeforeStart: if (book && chapter > 0) root.goTo(chapter - 1, -1, -1)
        onLaidOut: {
            if (reportOnLayout) { reportOnLayout = false; root.reportPosition() }
            else root.topChar = reader.topChar()
        }
        onViewportSettled: if (!initTimer.running) send(reqViewport, reader.viewport())
    }

    // ------------------------------------------------------------ error view
    ColumnLayout {
        visible: view === "error"
        anchors.centerIn: parent
        width: parent.width - 120
        spacing: 24
        Text { color: Theme.ink; text: "Something went wrong"; font.pixelSize: 36; font.bold: true; Layout.fillWidth: true; wrapMode: Text.Wrap }
        Text { color: Theme.ink; text: errorText; font.pixelSize: 28; Layout.fillWidth: true; wrapMode: Text.Wrap }
        RowLayout {
            spacing: 16
            EButton { text: "Library"; onClicked: toLibrary() }
            EButton { text: "Close"; onClicked: closeApp() }
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
                EButton { text: "‹ Library"; onClicked: toLibrary() }
                EButton { text: "‹ Back"; visible: backStack.length > 0; onClicked: goBack() }
                Item { Layout.fillWidth: true }
                EButton { text: "Close"; onClicked: closeApp() }
            }
            Column {
                Layout.fillWidth: true
                visible: view === "book" && !!book
                spacing: 4
                Text { color: Theme.ink; width: parent.width; text: book ? book.title : ""; font.pixelSize: 30; font.bold: true; elide: Text.ElideRight }
                Text {
                    width: parent.width
                    text: [book && book.author ? book.author : "", chapterTitle].filter(function (x) { return x !== "" }).join(" · ")
                    visible: text !== ""
                    font.pixelSize: 24; color: Theme.dim; elide: Text.ElideRight
                }
            }
            Flow {
                Layout.fillWidth: true
                spacing: 12
                visible: view === "book"
                EButton { text: "Contents"; onClicked: { panel = "contents"; menuOpen = false } }
                EButton { text: "Aa  Font"; onClicked: { menuOpen = false; panel = "font" } }
                EButton { text: "Sync"; visible: !!syncInfo.loggedIn; onClicked: { menuOpen = false; send(reqSync) } }
                EButton { text: "Settings"; onClicked: { panel = "settings"; menuOpen = false } }
            }
        }
    }
    Rectangle {
        id: menuBottom
        visible: menuOpen && view === "book"
        anchors.left: parent.left; anchors.right: parent.right; anchors.bottom: parent.bottom
        height: 120
        color: Theme.paper
        Rectangle { anchors.top: parent.top; width: parent.width; height: 3; color: Theme.ink }
        MouseArea { anchors.fill: parent }
        RowLayout {
            anchors.fill: parent
            anchors.margins: 24
            spacing: 16
            EButton { text: "‹"; fontSize: 40; enabled: reader.pageIndex > 0 || chapter > 0; onClicked: reader.prevPage() }
            Slider {
                id: pageSlider
                Layout.fillWidth: true
                from: 0; to: Math.max(1, reader.spreadStart(reader.pageCount - 1)); stepSize: reader.pageStep
                snapMode: Slider.SnapAlways
                enabled: reader.pageCount > 1
                value: reader.pageIndex
                onMoved: reader.goToPage(Math.round(value))
                background: Rectangle {
                    x: pageSlider.leftPadding
                    y: pageSlider.topPadding + pageSlider.availableHeight / 2 - height / 2
                    width: pageSlider.availableWidth; height: 6
                    color: Theme.ink
                }
                handle: Rectangle {
                    x: pageSlider.leftPadding + pageSlider.visualPosition * (pageSlider.availableWidth - width)
                    y: pageSlider.topPadding + pageSlider.availableHeight / 2 - height / 2
                    width: 44; height: 64; radius: 8
                    color: Theme.paper; border.color: Theme.ink; border.width: 4
                }
            }
            Text { color: Theme.ink;
                text: "Chapter " + (chapter + 1) + " of " + (book ? book.chapters : 0) + " · " + root.percent + "%"
                font.pixelSize: 24
            }
            EButton { text: "›"; fontSize: 40; enabled: !reader.lastSpread || (book && chapter + 1 < book.chapters); onClicked: reader.nextPage() }
        }
    }

    // ------------------------------------------------------------ contents panel
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
            Text {
                Layout.fillWidth: true
                text: book ? book.title : ""
                font.pixelSize: 24; color: Theme.dim; elide: Text.ElideRight
            }
            PagedList {
                id: tocList
                Layout.fillWidth: true
                Layout.fillHeight: true
                rowHeight: 84
                items: {
                    if (!book) return []
                    if (book.toc.length > 0) return book.toc
                    // no table of contents: the chapters
                    const out = []
                    for (let i = 0; i < book.chapters; i++)
                        if (book.counts[i] > 0) out.push({ title: "Chapter " + (i + 1), level: 1, chapter: i, char: 0 })
                    return out
                }
                emptyText: "This book has no table of contents."
                readonly property int current: {
                    let c = 0
                    for (let i = 0; i < items.length; i++)
                        if (items[i].chapter < chapter || (items[i].chapter === chapter && items[i].char <= topChar)) c = i
                    return c
                }
                onVisibleChanged: if (visible) page = Math.floor(current / perPage)
                rowDelegate: Item {
                    width: tocList.width
                    height: tocList.rowHeight
                    function tapped() {
                        root.remember()
                        closePanel()
                        root.goTo(modelData.chapter, modelData.char, 1)
                    }
                    Text { color: Theme.ink;
                        anchors.left: parent.left
                        anchors.leftMargin: Math.max(0, modelData.level - 1) * 36
                        anchors.right: pct.left; anchors.rightMargin: 20
                        anchors.verticalCenter: parent.verticalCenter
                        text: modelData.title
                        font.pixelSize: modelData.level <= 1 ? 29 : 26
                        font.bold: tocList.items.indexOf(modelData) === tocList.current
                        elide: Text.ElideRight
                    }
                    Text {
                        id: pct
                        anchors.right: parent.right
                        anchors.verticalCenter: parent.verticalCenter
                        text: Math.round(100 * root.percentAt(modelData.chapter, modelData.char)) + "%"
                        font.pixelSize: 24; color: Theme.dim
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
        defaults: appState.defaults || null
        showParagraphs: true
        showBookStyle: true
        onSetSetting: (obj) => root.setSetting(obj)
        onCloseRequested: root.closePanel()
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
                text: "Aa  Font and layout: " + Fonts.find(settings.font).label + ", " + (settings.fontSize || 34) + " px"
                onClicked: panel = "font"
            }
            Text { color: Theme.ink; text: "Pictures"; font.pixelSize: 30; font.bold: true }
            RowLayout {
                spacing: 16
                EButton { text: "Show"; active: !settings.noImages; onClicked: send(reqSettings, { noImages: false }) }
                EButton { text: "Hide"; active: !!settings.noImages; onClicked: send(reqSettings, { noImages: true }) }
                EButton { text: "Colour"; visible: !settings.noImages; active: !settings.greyImages; onClicked: send(reqSettings, { greyImages: false }) }
                EButton { text: "Grey"; visible: !settings.noImages; active: !!settings.greyImages; onClicked: send(reqSettings, { greyImages: true }) }
            }
            Text { color: Theme.ink; text: "Progress sync (KOReader)"; font.pixelSize: 30; font.bold: true }
            Text { color: Theme.ink;
                Layout.fillWidth: true
                wrapMode: Text.Wrap
                font.pixelSize: 24
                text: syncInfo.loggedIn ? "Logged in as " + syncInfo.username + " on " + String(syncInfo.server || "").replace(/^https?:\/\//, "") + "."
                                        : "Not logged in. Reading positions stay on this tablet."
            }
            Text { color: Theme.ink;
                Layout.fillWidth: true
                visible: !!syncInfo.status
                wrapMode: Text.Wrap
                font.pixelSize: 22; font.bold: true
                text: syncInfo.status || ""
            }
            RowLayout {
                spacing: 16
                EButton { text: syncInfo.loggedIn ? "Change login" : "Log in"; onClicked: openLogin() }
                EButton { text: "Log out"; visible: !!syncInfo.loggedIn; onClicked: send(reqLogout) }
            }
            // by hand, for the open book: the server's position (whatever it
            // is), or this one (even if unchanged)
            RowLayout {
                spacing: 16
                visible: !!syncInfo.loggedIn
                Text { color: Theme.ink; text: "This book"; font.pixelSize: 26; Layout.preferredWidth: 170 }
                EButton { text: "Pull"; enabled: view === "book" && !!book; onClicked: { closePanel(); send(reqPull) } }
                EButton { text: "Push"; enabled: view === "book" && !!book; onClicked: send(reqPush) }
                Text {
                    visible: !(view === "book" && book)
                    text: "Open a book first."
                    font.pixelSize: 22; color: Theme.dim
                }
            }
            RowLayout {
                spacing: 16
                visible: !!syncInfo.loggedIn
                Text { color: Theme.ink; text: "Automatic"; font.pixelSize: 26; Layout.preferredWidth: 170 }
                EButton { text: "On"; active: !!syncInfo.auto; onClicked: send(reqSettings, { autoSync: true }) }
                EButton { text: "Off"; active: !syncInfo.auto; onClicked: send(reqSettings, { autoSync: false }) }
            }
            Text {
                Layout.fillWidth: true
                wrapMode: Text.Wrap
                font.pixelSize: 22; color: Theme.dim
                text: "Opening a book jumps to a newer position from another device (KOReader, the built-in reader with kosync, ...); "
                      + "turning pages and closing the book send this one. Books are matched by their file, as in KOReader. "
                      + "The books are read by a sandboxed helper (" + (appState.sandbox || "starting") + ") that can read the library but not change it."
            }
            Text { text: "rm-books " + (appState.version || ""); font.pixelSize: 22; color: Theme.dim }
        }
    }

    // ------------------------------------------------------------ login panel
    function openLogin() {
        serverField.text = syncInfo.server || "https://sync.koreader.rocks"
        userField.text = syncInfo.username || ""
        passField.text = ""
        panel = "login"
        userField.forceActiveFocus()
    }
    function submitLogin(register) {
        const msg = { server: serverField.text.trim(), username: userField.text.trim(), register: register }
        if (passField.text !== "") msg.password = passField.text
        Qt.inputMethod.hide()
        root.forceActiveFocus()
        send(reqLogin, msg)
        panel = "settings"
    }
    Rectangle {
        visible: panel === "login"
        anchors.fill: parent
        color: Theme.paper
        MouseArea { anchors.fill: parent }
        ColumnLayout {
            anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top
            anchors.margins: 40
            spacing: 18
            RowLayout {
                Layout.fillWidth: true
                Text { color: Theme.ink; text: "Sync login"; font.pixelSize: 42; font.bold: true; Layout.fillWidth: true }
                EButton { text: "×"; fontSize: 40; onClicked: { closePanel(); panel = "settings" } }
            }
            Text { color: Theme.ink; text: "Server"; font.pixelSize: 26; font.bold: true }
            TextField {
                color: Theme.ink
                placeholderTextColor: Theme.dim
                id: serverField
                Layout.fillWidth: true
                implicitHeight: 80
                font.pixelSize: 28
                inputMethodHints: Qt.ImhNoAutoUppercase | Qt.ImhNoPredictiveText | Qt.ImhUrlCharactersOnly
                background: Rectangle { color: Theme.paper; border.color: Theme.ink; border.width: 3; radius: 10 }
            }
            Text { color: Theme.ink; text: "User name"; font.pixelSize: 26; font.bold: true }
            TextField {
                color: Theme.ink
                placeholderTextColor: Theme.dim
                id: userField
                Layout.fillWidth: true
                implicitHeight: 80
                font.pixelSize: 28
                inputMethodHints: Qt.ImhNoAutoUppercase | Qt.ImhNoPredictiveText
                background: Rectangle { color: Theme.paper; border.color: Theme.ink; border.width: 3; radius: 10 }
            }
            Text { color: Theme.ink; text: "Password"; font.pixelSize: 26; font.bold: true }
            TextField {
                color: Theme.ink
                placeholderTextColor: Theme.dim
                id: passField
                Layout.fillWidth: true
                implicitHeight: 80
                font.pixelSize: 28
                echoMode: TextInput.Password
                placeholderText: syncInfo.loggedIn && userField.text === syncInfo.username ? "(saved, type to change)" : ""
                inputMethodHints: Qt.ImhNoAutoUppercase | Qt.ImhNoPredictiveText | Qt.ImhSensitiveData
                background: Rectangle { color: Theme.paper; border.color: Theme.ink; border.width: 3; radius: 10 }
                onAccepted: submitLogin(false)
            }
            RowLayout {
                spacing: 16
                EButton { text: "Log in"; active: true; enabled: userField.text.trim() !== ""; onClicked: submitLogin(false) }
                EButton { text: "Register"; enabled: userField.text.trim() !== "" && passField.text !== ""; onClicked: submitLogin(true) }
            }
            Text {
                Layout.fillWidth: true
                wrapMode: Text.Wrap
                font.pixelSize: 22; color: Theme.dim
                text: "A KOReader sync server (kosync). Only a key made from the password is stored, as KOReader does."
            }
        }
    }

    // ------------------------------------------------------------ busy and messages
    Rectangle {
        visible: busyText !== ""
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.bottom: parent.bottom; anchors.bottomMargin: 140
        width: Math.min(parent.width - 80, busyLabel.implicitWidth + 48)
        height: busyLabel.implicitHeight + 32
        color: Theme.paper
        border.color: Theme.ink; border.width: 3; radius: 10
        MouseArea { anchors.fill: parent }
        Text { color: Theme.ink;
            id: busyLabel
            anchors.centerIn: parent
            width: Math.min(implicitWidth, root.width - 128)
            text: busyText; font.pixelSize: 26; elide: Text.ElideRight
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
}
