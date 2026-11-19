// Looks like a text field; tapping it opens the real one (a panel).
import QtQuick 2.15
import QtQuick.Layouts 1.15

Rectangle {
    id: fld
    property string text: ""
    property string placeholder: ""
    signal clicked
    implicitHeight: 80
    Layout.fillWidth: true
    color: Theme.paper
    border.color: Theme.ink; border.width: 3; radius: 10
    Text {
        anchors.fill: parent; anchors.leftMargin: 20; anchors.rightMargin: 20
        verticalAlignment: Text.AlignVCenter
        text: fld.text !== "" ? fld.text : fld.placeholder
        color: fld.text !== "" ? Theme.ink : Theme.dim
        font.pixelSize: 28
        elide: Text.ElideRight
    }
    MouseArea { anchors.fill: parent; onClicked: fld.clicked() }
}
