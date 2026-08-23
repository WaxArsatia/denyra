package main

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
)

func main() {
	data, err := os.ReadFile("internal/pipeline/adminui/assets/css/app.css")
	must(err)
	source := string(data)
	light := tokenBlock(source, `:root\{([^}]*)\}`)
	dark := tokenBlock(source, `@media\(prefers-color-scheme:dark\)\{:root\{([^}]*)\}`)
	for mode, tokens := range map[string]map[string]string{"light": light, "dark": dark} {
		for _, background := range []string{"surface", "surface-sunken"} {
			for _, foreground := range []string{"ink", "ink-muted", "ink-faint", "accent", "state-review", "state-blocked", "state-settled"} {
				ratio := contrast(tokens[foreground], tokens[background])
				if ratio < 4.5 {
					panic(fmt.Sprintf("%s %s on %s contrast %.2f < 4.5", mode, foreground, background, ratio))
				}
			}
		}
		if ratio := contrast(tokens["control-border"], tokens["surface"]); ratio < 3 {
			panic(fmt.Sprintf("%s control border contrast %.2f < 3", mode, ratio))
		}
	}
	fmt.Println("UI token contrast verified for light and dark themes")
}
func tokenBlock(source, pattern string) map[string]string {
	match := regexp.MustCompile(pattern).FindStringSubmatch(source)
	if len(match) != 2 {
		panic("token block missing: " + pattern)
	}
	tokens := map[string]string{}
	for _, pair := range strings.Split(match[1], ";") {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 && strings.HasPrefix(parts[0], "--") {
			tokens[strings.TrimPrefix(parts[0], "--")] = parts[1]
		}
	}
	return tokens
}
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + .05) / (lb + .05)
}
func luminance(hex string) float64 {
	if len(hex) != 7 || hex[0] != '#' {
		panic("invalid color " + hex)
	}
	var rgb [3]float64
	for i := range rgb {
		var value int
		_, err := fmt.Sscanf(hex[1+i*2:3+i*2], "%02x", &value)
		must(err)
		channel := float64(value) / 255
		if channel <= .04045 {
			channel /= 12.92
		} else {
			channel = math.Pow((channel+.055)/1.055, 2.4)
		}
		rgb[i] = channel
	}
	return .2126*rgb[0] + .7152*rgb[1] + .0722*rgb[2]
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
