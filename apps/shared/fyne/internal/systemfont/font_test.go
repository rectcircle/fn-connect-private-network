package systemfont

import (
	"encoding/binary"
	"testing"
)

func TestChooseConfiguredChineseSansNotSerif(t *testing.T) {
	xml := []byte(`<familyset><family name="sans-serif"><font weight="400">Roboto-Regular.ttf</font></family><family lang="zh-Hans"><font index="2" weight="400">NotoSansCJK.ttc</font><font index="2" weight="700">NotoSansCJK-Bold.ttc</font></family><family lang="zh-Hans"><font>NotoSerifCJK.ttc</font></family></familyset>`)
	for _, bold := range []bool{false, true} {
		font, err := Select(xml, bold)
		if err != nil || font.Index != 2 {
			t.Fatal(font, err)
		}
		if bold && font.File != "NotoSansCJK-Bold.ttc" {
			t.Fatal(font)
		}
	}
}
func TestCollectionRebasesSharedTableOffsets(t *testing.T) {
	data := make([]byte, 64)
	copy(data, "ttcf")
	binary.BigEndian.PutUint32(data[8:], 1)
	binary.BigEndian.PutUint32(data[12:], 16)
	binary.BigEndian.PutUint32(data[16:], 0x10000)
	binary.BigEndian.PutUint16(data[20:], 1)
	copy(data[28:], "test")
	binary.BigEndian.PutUint32(data[36:], 60)
	binary.BigEndian.PutUint32(data[40:], 4)
	copy(data[60:], "font")
	face, err := Face(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	offset := binary.BigEndian.Uint32(face[20:])
	if offset != 28 || string(face[offset:offset+4]) != "font" {
		t.Fatal("table was not rebased")
	}
	binary.BigEndian.PutUint32(data[40:], 1000)
	if _, err := Face(data, 0); err == nil {
		t.Fatal("accepted corrupt table")
	}
	if _, err := Face(data, 2); err == nil {
		t.Fatal("accepted invalid face")
	}
}
