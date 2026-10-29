// Package systemfont reads Android's sans-serif configuration and unwraps TTC
// faces for Fyne's single-face font loader. No fonts are bundled or downloaded.
package systemfont

import (
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
)

type Font struct {
	File   string `xml:",chardata"`
	Index  int    `xml:"index,attr"`
	Weight int    `xml:"weight,attr"`
	Style  string `xml:"style,attr"`
}
type Family struct {
	Name  string `xml:"name,attr"`
	Lang  string `xml:"lang,attr"`
	Fonts []Font `xml:"font"`
}
type Config struct {
	Families []Family `xml:"family"`
}

func Select(data []byte, bold bool) (Font, error) {
	var config Config
	if err := xml.Unmarshal(data, &config); err != nil {
		return Font{}, err
	}
	weight := 400
	if bold {
		weight = 700
	}
	bestScore := -1
	var selected Font
	for _, family := range config.Families {
		// The UI is Chinese. Prefer the configured Chinese sans fallback so CJK
		// cannot accidentally resolve to a decorative/serif family from a scan.
		priority := 0
		if family.Name == "sans-serif" {
			priority = 1000
		}
		if strings.Contains(family.Lang, "zh-Hans") {
			priority = 2000
		}
		if priority == 0 {
			continue
		}
		for _, font := range family.Fonts {
			if font.Style != "" && font.Style != "normal" {
				continue
			}
			name := strings.TrimSpace(font.File)
			if name == "" {
				continue
			}
			if strings.Contains(strings.ToLower(name), "serif") && !strings.Contains(strings.ToLower(name), "sans") {
				continue
			}
			distance := font.Weight - weight
			if distance < 0 {
				distance = -distance
			}
			score := priority - distance
			if score > bestScore {
				bestScore = score
				font.File = name
				selected = font
			}
		}
	}
	if bestScore < 0 {
		return Font{}, errors.New("system sans font not found")
	}
	return selected, nil
}
func Face(data []byte, index int) ([]byte, error) {
	if len(data) < 12 {
		return nil, errors.New("truncated font")
	}
	if string(data[:4]) != "ttcf" {
		if index != 0 {
			return nil, errors.New("single font has no requested face")
		}
		if string(data[:4]) != "OTTO" && binary.BigEndian.Uint32(data[:4]) != 0x10000 {
			return nil, errors.New("invalid font header")
		}
		return data, nil
	}
	count := int(binary.BigEndian.Uint32(data[8:12]))
	if index < 0 || index >= count || count > (len(data)-12)/4 {
		return nil, errors.New("invalid font collection index")
	}
	offset := int(binary.BigEndian.Uint32(data[12+index*4:]))
	if offset < 0 || offset > len(data)-12 {
		return nil, errors.New("invalid face offset")
	}
	tables := int(binary.BigEndian.Uint16(data[offset+4:]))
	if tables == 0 || tables > (len(data)-offset-12)/16 {
		return nil, errors.New("invalid table directory")
	}
	// Allocate once: CJK tables are large and repeated append growth caused
	// avoidable copying and GC work during a cold launch.
	total := 12 + tables*16
	for i := 0; i < tables; i++ {
		entry := data[offset+12+i*16 : offset+28+i*16]
		source := uint64(binary.BigEndian.Uint32(entry[8:]))
		size := uint64(binary.BigEndian.Uint32(entry[12:]))
		if source+size > uint64(len(data)) {
			return nil, fmt.Errorf("invalid font table %d", i)
		}
		total += int((size + 3) &^ 3)
	}
	out := make([]byte, 12+tables*16, total)
	copy(out, data[offset:offset+12+tables*16])
	head := -1
	for i := 0; i < tables; i++ {
		entry := out[12+i*16 : 12+(i+1)*16]
		source := int(binary.BigEndian.Uint32(entry[8:]))
		size := int(binary.BigEndian.Uint32(entry[12:]))
		if source < 0 || size < 0 || source > len(data) || size > len(data)-source {
			return nil, fmt.Errorf("invalid font table %d", i)
		}
		target := len(out)
		binary.BigEndian.PutUint32(entry[8:], uint32(target))
		tag := string(entry[:4])
		out = append(out, data[source:source+size]...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		if tag == "head" && size >= 12 {
			head = target
			binary.BigEndian.PutUint32(out[head+8:], 0)
		}
	}
	if head >= 0 {
		var sum uint32
		for i := 0; i < len(out); i += 4 {
			sum += binary.BigEndian.Uint32(out[i:])
		}
		binary.BigEndian.PutUint32(out[head+8:], 0xb1b0afba-sum)
	}
	return out, nil
}
