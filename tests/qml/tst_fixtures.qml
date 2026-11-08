// Real pages from the web (made by the backend's live test with
// RM_BROWSER_FIXTURE=build/fixture): paginate each one, check that no line
// is cut, and save screenshots. Skipped when there are no fixtures.
import QtQuick 2.15
import QtTest 1.2
import net.asivery.AppLoad 1.0

Item {
    id: win
    width: 954; height: 1696

    Loader { id: loader; anchors.fill: parent; source: "../../app/ui/main.qml" }

    TestCase {
        name: "RealPages"
        when: windowShown && loader.status === Loader.Ready
        property var app: loader.item
        readonly property string dir: Qt.resolvedUrl("../../build/fixture/").toString()
        readonly property string outDir: Qt.resolvedUrl("../../build/qmltest/").toString().replace("file://", "")

        function read(url) {
            const x = new XMLHttpRequest()
            x.open("GET", url, false)
            x.send()
            return x.responseText
        }
        function shot(name) {
            let done = false
            app.grabToImage(function (r) { r.saveToFile(outDir + name + ".png"); done = true })
            tryVerify(function () { return done }, 3000)
        }

        function test_pages() {
            const list = read(dir + "index.txt").split("\n").filter(function (l) { return l.trim() !== "" })
            if (list.length === 0) skip("no fixtures")
            Bus.deliver(101, { settings: { fontSize: 32, serif: false, safeSearch: "moderate", noImages: false }, history: [], imageDir: "/repo/build/fixture/cache/img" })
            const te = findChild(app, "te")
            for (let n = 0; n < list.length; n++) {
                const page = JSON.parse(read(dir + list[n]))
                Bus.deliver(103, { page: page, position: 0 })
                tryVerify(function () { return app.pageCount > 1 && te.contentHeight > 100 }, 5000, list[n])
                wait(300)   // images
                const H = Math.floor(findChild(app, "readerArea").height)
                for (let i = 1; i < app.pageCount; i++) {
                    const s = app.pageStarts[i]
                    const r = te.positionToRectangle(te.positionAt(4, s + 1))
                    verify(r.y >= s - 1, list[n] + ": page " + i + " starts inside a line")
                    verify(app.pageHeights[i - 1] <= H)
                }
                let found = 0
                for (const h of app.headingPages) if (h >= 0) found++
                console.log(list[n].substring(0, 70), "pages:", app.pageCount, "headings found:", found + "/" + page.headings.length)
                const base = "real-" + n
                shot(base + "-p1")
                app.goToPage(1)
                wait(200)
                shot(base + "-p2")
            }
        }
    }
}
