"""Generate app/icon.png (a globe over a page of text) with only the stdlib.

Black on white for e-ink, anti-aliased by 4x4 supersampling.
"""
import math, struct, sys, zlib

W = H = 128
SS = 4
CX, CY, R = 64, 58, 44   # globe
LW = 4.5                 # line width


def ring(d, r, w):
    return abs(d - r) <= w / 2


def ink(x, y):
    dx, dy = x - CX, y - CY
    d = math.hypot(dx, dy)
    if ring(d, R, LW + 1):
        return True
    if d < R:
        # meridians: ellipses through the poles
        for rx in (R * 0.42, ):
            e = math.hypot(dx / rx, dy / R)
            if abs(e - 1) * rx <= LW / 2:
                return True
        if abs(dx) <= LW / 2:
            return True
        # equator and two parallels
        for py in (0, -R * 0.5, R * 0.5):
            if abs(dy - py) <= LW / 2 - 0.5:
                return True
    # two lines of text under the globe
    for i, (ly, ln) in enumerate(((110, 76), (120, 56))):
        if abs(y - ly) <= 2.6 and CX - ln / 2 <= x <= CX + ln / 2:
            return True
    return False


rows = []
for py in range(H):
    row = bytearray([0])
    for px in range(W):
        n = sum(ink(px + (i + 0.5) / SS, py + (j + 0.5) / SS) for i in range(SS) for j in range(SS))
        row.append(255 - round(255 * n / (SS * SS)))
    rows.append(bytes(row))


def chunk(t, data):
    c = struct.pack(">I", len(data)) + t + data
    return c + struct.pack(">I", zlib.crc32(t + data) & 0xFFFFFFFF)


png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", W, H, 8, 0, 0, 0, 0))
png += chunk(b"IDAT", zlib.compress(b"".join(rows), 9)) + chunk(b"IEND", b"")
out = sys.argv[1] if len(sys.argv) > 1 else "app/icon.png"
with open(out, "wb") as f:
    f.write(png)
print("wrote", out)
