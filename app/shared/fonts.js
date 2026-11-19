// The reader's typefaces (the Font panel). Noto Sans and EB Garamond come
// with the tablet; the others come with the apps (SIL OFL, app/fonts).
// The browser embeds the same files for Save as PDF (backend/fonts,
// pdfFonts in export.go) and the backends accept these keys
// (backend/internal/typography): keep all of them in step.
.pragma library

var choices = [
    { key: "sans", label: "Noto Sans", family: "Noto Sans", group: "sans" },
    { key: "inter", label: "Inter", family: "Inter", file: "Inter", group: "sans" },
    { key: "plexsans", label: "Plex Sans", family: "IBM Plex Sans", file: "IBMPlexSans", group: "sans" },
    { key: "atkinson", label: "Atkinson", family: "Atkinson Hyperlegible", file: "AtkinsonHyperlegible", group: "sans" },
    { key: "garamond", label: "Garamond", family: "EB Garamond", group: "serif" },
    { key: "literata", label: "Literata", family: "Literata", file: "Literata", group: "serif" },
    { key: "merriweather", label: "Merriweather", family: "Merriweather", file: "Merriweather", group: "serif" },
    { key: "sourceserif", label: "Source Serif", family: "Source Serif 4", file: "SourceSerif4", group: "serif" },
    { key: "lora", label: "Lora", family: "Lora", file: "Lora", group: "serif" },
    { key: "crimson", label: "Crimson", family: "Crimson Pro", file: "CrimsonPro", group: "serif" },
    { key: "bitter", label: "Bitter", family: "Bitter", file: "Bitter", group: "serif" },
    { key: "plexmono", label: "Plex Mono", family: "IBM Plex Mono", file: "IBMPlexMono", group: "mono" }
]

var groups = [{ key: "sans", title: "Sans serif" }, { key: "serif", title: "Serif" }, { key: "mono", title: "Monospace" }]

function find(key) {
    for (var i = 0; i < choices.length; i++) if (choices[i].key === (key || "sans")) return choices[i]
    return choices[0]
}

function familyOf(key) { return find(key).family }

// the font files to load (relative to app/fonts)
function files() {
    var out = []
    var styles = ["Regular", "Bold", "Italic", "BoldItalic"]
    for (var i = 0; i < choices.length; i++)
        if (choices[i].file) for (var s = 0; s < styles.length; s++) out.push(choices[i].file + "-" + styles[s] + ".ttf")
    return out
}
