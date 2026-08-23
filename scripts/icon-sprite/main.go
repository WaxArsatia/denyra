package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type svg struct {
	ViewBox string `xml:"viewBox,attr"`
	Inner   string `xml:",innerxml"`
}

func main() {
	const source = "internal/pipeline/adminui/assets/vendor/icons"
	entries, err := os.ReadDir(source)
	must(err)
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && filepath.Ext(entry.Name()) == ".svg" {
			names = append(names, strings.TrimSuffix(entry.Name(), ".svg"))
		}
	}
	sort.Strings(names)
	var output bytes.Buffer
	output.WriteString(`<svg xmlns="http://www.w3.org/2000/svg"><defs>`)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(source, name+".svg"))
		must(err)
		var sourceSVG svg
		must(xml.Unmarshal(data, &sourceSVG))
		if sourceSVG.ViewBox == "" || strings.TrimSpace(sourceSVG.Inner) == "" {
			panic("invalid SVG source: " + name)
		}
		fmt.Fprintf(&output, `<symbol id="%s" viewBox="%s">%s</symbol>`, name, sourceSVG.ViewBox, sourceSVG.Inner)
	}
	output.WriteString(`</defs></svg>`)
	must(os.WriteFile("internal/pipeline/adminui/assets/vendor/icons.svg", output.Bytes(), 0o644))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
