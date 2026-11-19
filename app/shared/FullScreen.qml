// Reading only makes sense full screen, so hide the minimise and maximise
// buttons of AppLoad's window bar (only close stays). AppLoad offers this
// through the manifest only for native apps, so find our AppLoad window up
// the parent chain and set its flag ourselves. If a future AppLoad changes
// this, nothing is found and nothing happens.
import QtQuick 2.15

Item {
    property var appWindow: null
    function start(from) {
        for (let p = from; p; p = p.parent) {
            if (typeof p.disablesWindowedMode === "boolean") {
                appWindow = p
                p.disablesWindowedMode = true
                check.start()
                return
            }
        }
    }
    Timer {
        id: check
        interval: 1500
        onTriggered: {
            if (appWindow && appWindow.fullscreen === false && typeof appWindow.maximize === "function")
                appWindow.maximize()
        }
    }
}
