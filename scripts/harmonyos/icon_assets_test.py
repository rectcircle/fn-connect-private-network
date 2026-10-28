"""Validate store-facing layered icons without image-library dependencies."""
import json
from pathlib import Path
import struct
import unittest
import zlib

ROOT = Path(__file__).resolve().parents[2]


def png_alpha(path):
    data = path.read_bytes()
    assert data[:8] == b'\x89PNG\r\n\x1a\n'
    size = struct.unpack('>IIBBBBB', data[16:29])
    width, height, depth, color, _, _, interlace = size
    assert (width, height, depth, interlace) == (1024, 1024, 8, 0), size
    assert color in (2, 6), color
    channels = 4 if color == 6 else 3
    compressed = b''
    offset = 8
    while offset < len(data):
        length = int.from_bytes(data[offset:offset+4], 'big')
        if data[offset+4:offset+8] == b'IDAT': compressed += data[offset+8:offset+8+length]
        offset += length + 12
    raw = zlib.decompress(compressed)
    if channels == 3: return bytes([255]) * (width * height)
    stride = width * channels
    previous = bytearray(width)
    alpha = bytearray()
    for y in range(height):
        line = raw[y*(stride+1):(y+1)*(stride+1)]
        mode = line[0]
        current = bytearray(width)
        for x in range(width):
            value = line[1+x*channels+3]
            a = current[x-1] if x else 0
            b = previous[x]
            c = previous[x-1] if x else 0
            if mode == 1: value += a
            elif mode == 2: value += b
            elif mode == 3: value += (a+b)//2
            elif mode == 4:
                p = a+b-c
                distances = [abs(p-a), abs(p-b), abs(p-c)]
                value += [a,b,c][distances.index(min(distances))]
            else: assert mode == 0
            current[x] = value & 255
        alpha.extend(current)
        previous = current
    return alpha


class LayeredIconTests(unittest.TestCase):
    def test_layers_are_full_size_and_background_has_no_mask_or_padding(self):
        for relative in ['AppScope/resources/base/media', 'entry/src/main/resources/base/media']:
            media = ROOT/'apps/harmonyos'/relative
            layers = json.loads((media/'layered_image.json').read_text())['layered-image']
            self.assertEqual(layers, {'background':'$media:icon_background', 'foreground':'$media:icon_foreground'})
            background = png_alpha(media/'icon_background.png')
            self.assertEqual(set(background), {255}, 'background must cover every pixel, including corners')
            foreground = png_alpha(media/'icon_foreground.png')
            self.assertEqual((min(foreground), max(foreground)), (0, 255))
            self.assertEqual(foreground[0], 0)

    def test_launcher_and_ability_icons_use_layers(self):
        app = json.loads((ROOT/'apps/harmonyos/AppScope/app.json5').read_text())['app']
        self.assertEqual(app['icon'], '$media:layered_image')
        module = json.loads((ROOT/'apps/harmonyos/entry/src/main/module.json5').read_text())['module']
        for ability in module['abilities']:
            self.assertEqual(ability['icon'], '$media:layered_image')
            self.assertEqual(ability['startWindowIcon'], '$media:layered_image')


if __name__ == '__main__': unittest.main()
