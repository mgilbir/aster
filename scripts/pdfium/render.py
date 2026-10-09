# Renders the first page of a PDF with PDFium, Chrome's PDF engine, through
# pypdfium2, for the tests that read aster's PDFs back (ASTER_PDFIUM):
#
#   python3 scripts/pdfium/render.py in.pdf out.rgba [scale]
#
# out.rgba is a header line, "width height", then width*height RGBA pixels,
# so that nothing but pypdfium2 is needed to write it. The page is drawn on
# white, at scale pixels a point (1 by default).
import sys

import pypdfium2 as pdfium

pdf = pdfium.PdfDocument(sys.argv[1])
scale = float(sys.argv[3]) if len(sys.argv) > 3 else 1.0
bitmap = pdf[0].render(scale=scale, fill_color=(255, 255, 255, 255), rev_byteorder=True, prefer_bgrx=False)
w, h, stride, n = bitmap.width, bitmap.height, bitmap.stride, bitmap.n_channels
buf = bytes(bitmap.buffer)
with open(sys.argv[2], "wb") as out:
    out.write(f"{w} {h}\n".encode())
    for y in range(h):
        row = buf[y * stride:y * stride + w * n]
        if n == 4:
            out.write(row)
        else:
            out.write(b"".join(row[x * n:x * n + 3] + b"\xff" for x in range(w)))
