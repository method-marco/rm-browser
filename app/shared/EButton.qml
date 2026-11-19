// A button for e-ink: black frame, black when active, no animation.
import QtQuick 2.15

Rectangle {
    id: btn
    property alias text: lbl.text
    property bool active: false
    property int fontSize: 28
    property string fontFamily: ""
    signal clicked
    implicitWidth: Math.max(100, lbl.implicitWidth + 36)
    implicitHeight: 68
    color: active ? Theme.ink : Theme.paper
    border.color: Theme.ink; border.width: 3; radius: 8
    opacity: enabled ? 1 : 0.3
    Text { id: lbl; anchors.centerIn: parent; font.pixelSize: btn.fontSize; font.family: btn.fontFamily || "Noto Sans"; color: btn.active ? Theme.paper : Theme.ink }
    MouseArea { anchors.fill: parent; onClicked: btn.clicked() }
}
