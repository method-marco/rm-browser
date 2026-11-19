// A list shown page by page (no scrolling): as many rows as fit, with
// buttons to turn the page. Swiping left/right turns it too.
import QtQuick 2.15
import QtQuick.Layouts 1.15

ColumnLayout {
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
            font.pixelSize: 26; color: Theme.dim
        }
        // swipe to turn; a tap goes to the row under it (row.tapped()).
        // Never propagateComposedEvents here: Qt hands the click on to
        // every MouseArea under the finger in the whole window, which
        // includes xochitl's behind the app (AppLoad's list of apps, the
        // library): a tap on a row also launched an app or a notebook.
        MouseArea {
            anchors.fill: parent
            property real px: 0
            property real py: 0
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
        Text { color: Theme.ink;
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
