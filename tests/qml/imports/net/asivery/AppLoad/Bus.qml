// Test stub: connects the test to the app's AppLoad object.
pragma Singleton
import QtQuick 2.15

QtObject {
    property var backend: null    // the app's AppLoad instance
    property var sent: []         // [type, parsed JSON] sent by the app
    function deliver(type, obj) { backend.messageReceived(type, JSON.stringify(obj)) }
    function lastOf(type) {
        for (let i = sent.length - 1; i >= 0; i--) if (sent[i][0] === type) return sent[i][1]
        return null
    }
    function clear() { sent = [] }
}
