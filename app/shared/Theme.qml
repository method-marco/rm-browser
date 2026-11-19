// The colours of both apps (the Font panel's Theme dropdown). The
// reMarkable's front light is cool white: the warm themes (cream, warm,
// sepia, night shift) show warmer paper on the Paper Pro's colour screen;
// soft dark is light grey on dark grey, dark white on black. Set from the
// settings by each app; the backends accept the same keys
// (backend/internal/typography).
pragma Singleton
import QtQuick 2.15

QtObject {
    property string theme: "default"
    readonly property string current: themes[theme] ? theme : "default"

    // paper, text, secondary text, rules and frames of rows
    readonly property var themes: ({
        "default":    { label: "Default",     paper: "#ffffff", ink: "#000000", dim: "#444444", line: "#999999" },
        "cream":      { label: "Cream",       paper: "#fbf3e3", ink: "#000000", dim: "#4a4238", line: "#a89f90" },
        "warm":       { label: "Warm",        paper: "#f6e7c9", ink: "#1a1208", dim: "#4e4232", line: "#a8977a" },
        "sepia":      { label: "Sepia",       paper: "#eed8b0", ink: "#3a2814", dim: "#5c4630", line: "#a88c66" },
        "nightshift": { label: "Night shift", paper: "#e8c994", ink: "#000000", dim: "#4a3a22", line: "#9c8156" },
        "softdark":   { label: "Soft dark",   paper: "#3a3a3a", ink: "#e6e6e6", dim: "#b4b4b4", line: "#6e6e6e" },
        "dark":       { label: "Dark",        paper: "#000000", ink: "#ffffff", dim: "#bbbbbb", line: "#666666" }
    })
    readonly property var keys: ["default", "cream", "warm", "sepia", "nightshift", "softdark", "dark"]
    readonly property var colors: themes[current]
    readonly property color paper: colors.paper
    readonly property color ink: colors.ink
    readonly property color dim: colors.dim
    readonly property color line: colors.line
}
