// Test stub of AppLoad's backend object: records what the app sends.
import QtQuick 2.15

QtObject {
    property string applicationID
    signal messageReceived(int type, string contents)
    function sendMessage(type, contents) {
        const s = Bus.sent.slice()
        s.push([type, contents ? JSON.parse(contents) : {}])
        Bus.sent = s
    }
    function terminate() {}
    Component.onCompleted: Bus.backend = this
}
