// Test stub: a fake rm-librarian library (folder "Web" and its documents).
pragma Singleton
import QtQuick 2.15

QtObject {
    property bool installed: true
    property var calls: []        // [signal, message]
    property var docs: []         // names in "Web"
    property var imported: []     // file paths imported
    property bool renamed: false  // renameEntry was called (must never be)
    function reset() { calls = []; docs = []; imported = []; installed = true; renamed = false }
    function handle(signal, message) {
        calls = calls.concat([[signal, message]])
        if (!installed) return ""
        switch (signal) {
        case "ensureFolder": return "folder-web-uuid"
        case "lookupEntry": {
            const name = message.replace(/^Web\//, "")
            return docs.indexOf(name) >= 0 ? "doc-" + docs.indexOf(name) : "ERROR: Entry not found"
        }
        case "importDocument": {
            const i = message.lastIndexOf(",")
            if (message.slice(i + 1) !== "folder-web-uuid") return "ERROR: bad folder"
            const path = message.slice(0, i)
            imported = imported.concat([path])
            docs = docs.concat([path.slice(path.lastIndexOf("/") + 1).replace(/\.pdf$/, "")])
            return "new-doc-" + (docs.length - 1)
        }
        case "renameEntry": {   // would deadlock xochitl from the UI thread
            renamed = true
            const i = message.lastIndexOf(",")
            const idx = parseInt(message.slice(0, i).replace("new-doc-", ""))
            const d = docs.slice(); d[idx] = message.slice(i + 1); docs = d
            return "ok"
        }
        }
        return "ERROR: unknown"
    }
}
