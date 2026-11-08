// Test stub of xovi-message-broker with a fake rm-librarian behind it.
import QtQuick 2.15

QtObject {
    property var listeningFor: []
    function sendSimpleSignal(signal, message) { return Library.handle(signal, message) }
}
