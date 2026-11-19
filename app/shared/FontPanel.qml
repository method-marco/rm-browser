// Font and layout, at the bottom of the screen so that the page above shows
// each change right away. Shared by the browser and Books; the app sends
// each change to its backend (setSetting) and the page follows when the
// backend confirms.
import QtQuick 2.15
import QtQuick.Layouts 1.15
import "fonts.js" as Fonts

Item {
    id: fp
    property var settings: ({})
    property bool open: false
    property var defaults: null          // the app's defaults: shows "Defaults" (reset)
    property bool showParagraphs: false  // the paragraph style row (books)
    property bool showBookStyle: false   // the book's own formatting or plain (books)
    signal setSetting(var obj)
    signal closeRequested()

    readonly property int minFontSize: 16
    readonly property int maxFontSize: 96
    readonly property int size: settings.fontSize || 32
    readonly property int lineSpacing: settings.lineSpacing || 125
    readonly property bool justified: settings.align === "justify"
    property bool fontListOpen: false
    property bool themeListOpen: false
    onOpenChanged: { fontListOpen = false; themeListOpen = false }
    visible: open
    anchors.fill: parent

    // finer steps for small text, bigger ones for large
    function fontStep(up) { return (up ? size >= 40 : size > 40) ? 4 : 2 }
    function isDefault() {
        if (!defaults) return true
        for (const k in defaults) if (settings[k] !== defaults[k]) return false
        return true
    }

    MouseArea {   // tap on the page above closes it
        anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top
        anchors.bottom: fontPanel.top
        onClicked: fp.closeRequested()
    }
    Rectangle {
        id: fontPanel
        objectName: "fontPanel"
        anchors.left: parent.left; anchors.right: parent.right; anchors.bottom: parent.bottom
        height: fontCol.implicitHeight + 56
        color: Theme.paper
        Rectangle { anchors.top: parent.top; width: parent.width; height: 3; color: Theme.ink }
        MouseArea { anchors.fill: parent }
        ColumnLayout {
            id: fontCol
            anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top
            anchors.margins: 28
            spacing: 14
            RowLayout {
                Layout.fillWidth: true
                spacing: 12
                Text { color: Theme.ink; text: "Font and layout"; font.pixelSize: 36; font.bold: true; Layout.fillWidth: true }
                EButton { text: "Defaults"; visible: !!fp.defaults; enabled: !fp.isDefault(); onClicked: fp.setSetting(fp.defaults) }
                EButton { text: "×"; fontSize: 40; onClicked: fp.closeRequested() }
            }
            // the book's own formatting (its CSS: alignment, indents, spacing,
            // emphasis, sizes) or the reader's plain one
            RowLayout {
                spacing: 12
                visible: !fp.fontListOpen && !fp.themeListOpen && fp.showBookStyle
                Text { color: Theme.ink; text: "Formatting"; font.pixelSize: 26; font.bold: true; Layout.preferredWidth: 170 }
                EButton { text: "Book's"; active: fp.settings.bookStyle !== false; onClicked: fp.setSetting({ bookStyle: true }) }
                EButton { text: "Plain"; active: fp.settings.bookStyle === false; onClicked: fp.setSetting({ bookStyle: false }) }
            }
            // the typeface: one button showing the current one, opening the
            // list (each name in its own face) in place of the other rows
            RowLayout {
                spacing: 12
                visible: !fp.fontListOpen && !fp.themeListOpen
                Text { color: Theme.ink; text: "Font"; font.pixelSize: 26; font.bold: true; Layout.preferredWidth: 170 }
                EButton {
                    objectName: "fontDropdown"
                    Layout.fillWidth: true
                    text: Fonts.find(fp.settings.font).label + "  ▾"
                    fontFamily: Fonts.find(fp.settings.font).family
                    onClicked: fp.fontListOpen = true
                }
            }
            Repeater {
                model: fp.fontListOpen ? Fonts.groups : []
                ColumnLayout {
                    Layout.fillWidth: true
                    spacing: 10
                    required property var modelData
                    Text { color: Theme.ink; text: modelData.title; font.pixelSize: 24; font.bold: true }
                    GridLayout {
                        Layout.fillWidth: true
                        columns: 2; columnSpacing: 12; rowSpacing: 12
                        Repeater {
                            model: Fonts.choices.filter(function (f) { return f.group === modelData.key })
                            EButton {
                                required property var modelData
                                Layout.fillWidth: true
                                Layout.preferredWidth: 1
                                text: modelData.label
                                fontFamily: modelData.family
                                fontSize: 30
                                active: (fp.settings.font || "sans") === modelData.key
                                onClicked: { fp.setSetting({ font: modelData.key }); fp.fontListOpen = false }
                            }
                        }
                    }
                }
            }
            RowLayout {
                spacing: 12
                visible: !fp.fontListOpen && !fp.themeListOpen
                Text { color: Theme.ink; text: "Size"; font.pixelSize: 26; font.bold: true; Layout.preferredWidth: 170 }
                // fp.size: inside an EButton, fontSize is the button's own label size
                EButton { text: "A−"; fontSize: 24; enabled: fp.size > fp.minFontSize; onClicked: fp.setSetting({ fontSize: Math.max(fp.minFontSize, fp.size - fp.fontStep(false)) }) }
                Text { color: Theme.ink; text: fp.size + " px"; font.pixelSize: 26; Layout.preferredWidth: 100; horizontalAlignment: Text.AlignHCenter }
                EButton { text: "A+"; fontSize: 32; enabled: fp.size < fp.maxFontSize; onClicked: fp.setSetting({ fontSize: Math.min(fp.maxFontSize, fp.size + fp.fontStep(true)) }) }
            }
            RowLayout {
                spacing: 12
                visible: !fp.fontListOpen && !fp.themeListOpen
                Text { color: Theme.ink; text: "Spacing"; font.pixelSize: 26; font.bold: true; Layout.preferredWidth: 170 }
                Repeater {
                    model: [[100, "1.0"], [115, "1.15"], [125, "1.25"], [150, "1.5"], [175, "1.75"], [200, "2.0"]]
                    EButton { text: modelData[1]; fontSize: 24; implicitWidth: 92; active: fp.lineSpacing === modelData[0]; onClicked: fp.setSetting({ lineSpacing: modelData[0] }) }
                }
            }
            RowLayout {
                spacing: 12
                visible: !fp.fontListOpen && !fp.themeListOpen
                Text { color: Theme.ink; text: "Alignment"; font.pixelSize: 26; font.bold: true; Layout.preferredWidth: 170 }
                EButton { text: "Left"; active: !fp.justified; onClicked: fp.setSetting({ align: "left" }) }
                EButton { text: "Justified"; active: fp.justified; onClicked: fp.setSetting({ align: "justify" }) }
            }
            RowLayout {
                spacing: 12
                visible: !fp.fontListOpen && !fp.themeListOpen && fp.showParagraphs
                Text { color: Theme.ink; text: "Paragraphs"; font.pixelSize: 26; font.bold: true; Layout.preferredWidth: 170 }
                EButton { text: "Indented"; active: fp.settings.paragraphs === "indent"; onClicked: fp.setSetting({ paragraphs: "indent" }) }
                EButton { text: "Spaced"; active: fp.settings.paragraphs !== "indent"; onClicked: fp.setSetting({ paragraphs: "spaced" }) }
            }
            // colours (Theme.qml): one button showing the current theme,
            // opening the list (each shown in its own colours)
            RowLayout {
                spacing: 12
                visible: !fp.fontListOpen && !fp.themeListOpen
                Text { color: Theme.ink; text: "Theme"; font.pixelSize: 26; font.bold: true; Layout.preferredWidth: 170 }
                EButton {
                    objectName: "themeDropdown"
                    Layout.fillWidth: true
                    text: Theme.themes[Theme.current].label + "  \u25BE"
                    onClicked: fp.themeListOpen = true
                }
            }
            Repeater {
                model: fp.themeListOpen ? Theme.keys : []
                Rectangle {
                    required property var modelData
                    readonly property var t: Theme.themes[modelData]
                    readonly property bool active: Theme.current === modelData
                    property alias text: lbl.text      // (tests find it like a button)
                    signal clicked
                    onClicked: { fp.setSetting({ theme: modelData }); fp.themeListOpen = false }
                    Layout.fillWidth: true
                    implicitHeight: 64
                    radius: 8
                    color: t.paper
                    border.color: Theme.ink; border.width: active ? 6 : 3
                    Text { id: lbl; anchors.centerIn: parent; text: t.label; color: t.ink; font.pixelSize: 28; font.bold: parent.active }
                    MouseArea { anchors.fill: parent; onClicked: parent.clicked() }
                }
            }
            RowLayout {
                spacing: 12
                visible: !fp.fontListOpen && !fp.themeListOpen
                Text { color: Theme.ink; text: "Margins"; font.pixelSize: 26; font.bold: true; Layout.preferredWidth: 170 }
                EButton { text: "Narrow"; active: fp.settings.margin === "narrow"; onClicked: fp.setSetting({ margin: "narrow" }) }
                EButton { text: "Normal"; active: !fp.settings.margin || fp.settings.margin === "normal"; onClicked: fp.setSetting({ margin: "normal" }) }
                EButton { text: "Wide"; active: fp.settings.margin === "wide"; onClicked: fp.setSetting({ margin: "wide" }) }
            }
        }
    }
}
