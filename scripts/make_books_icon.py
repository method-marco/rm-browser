"""Generate app/books/icon.png (an open book with lines of text) with only
the stdlib, in the style of the browser's icon (scripts/make_icon.py).

Black on white for e-ink, anti-aliased by 4x4 supersampling.
"""
import struct, sys, zlib

W = H = 128
SS = 4
LW = 4.5
TOP, BOTTOM = 30, 98     # page edges at the spine
SAG = 8                  # the outer corners sit this much higher


def page_top(x):
    return TOP - SAG * abs(x - 64) / 54


def page_bottom(x):
    return BOTTOM - SAG * abs(x - 64) / 54


def ink(x, y):
    # outline of both pages
    if 10 <= x <= 118:
        if abs(y - page_top(x)) <= LW / 2 or abs(y - page_bottom(x)) <= LW / 2:
            return True
    for ex in (10, 118):
        if abs(x - ex) <= LW / 2 and page_top(ex) - LW / 2 <= y <= page_bottom(ex) + LW / 2:
            return True
    # spine
    if abs(x - 64) <= LW / 2 and TOP <= y <= BOTTOM:
        return True
    # lines of text on each page
    for x0, x1 in ((20, 56), (72, 108)):
        if x0 <= x <= x1:
            for i in range(5):
                off = 14 + i * 11
                if abs(y - (page_top(x) + off)) <= 1.6:
                    if i == 4 and (x > x0 + 22):
                        continue   # a short last line
                    return True
    # a base line under the book, like the browser's icon
    if abs(y - 112) <= 2.6 and 34 <= x <= 94:
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
out = sys.argv[1] if len(sys.argv) > 1 else "app/books/icon.png"
with open(out, "wb") as f:
    f.write(png)
print("wrote", out)
