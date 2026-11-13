package web

import (
	"fmt"
	"html/template"
	"sort"
	"strings"
)

// Pixel-art icons (D11, D12): 16 rows of 16 cells, one character per cell, drawn as SVG squares
// with crisp edges. "." is empty; every other character names a colour in iconPalette.
var iconPalette = map[byte]string{
	'k': "#1a1a1a", // outline
	'w': "#ffffff",
	'g': "#d9d9d9", // light grey
	'G': "#9a9a9a", // grey
	's': "#7eba94", // sage
	'S': "#4f8f66", // dark sage
	'c': "#f98b8a", // coral
	'C': "#d9483b", // red
	'y': "#f5bd3f", // mustard
	'Y': "#d99a1e", // deep yellow
	'b': "#57c9fb", // sky
	'B': "#2b8fd6", // deep blue
	'n': "#c08552", // brown
	'N': "#7a4a26", // dark brown
	'p': "#fae0e1", // blush
	'o': "#f08a3c", // orange
}

// iconGrids holds every icon by key. The keys are the values of icon: in services.yml.
var iconGrids = map[string][16]string{
	// The app itself: a magnifying glass (D12).
	"inspect": {
		"................",
		"....kkkkk.......",
		"...kGGGGGk......",
		"..kGbbwbbGk.....",
		".kGbbwbbbbGk....",
		".kGbwbbbbbGk....",
		".kGbbbbbbbGk....",
		".kGbbbbbbbGk....",
		".kGbbbbbbbGk....",
		"..kGbbbbbGk.....",
		"...kGGGGGkk.....",
		"....kkkkkNnk....",
		"..........kNnk..",
		"...........kNnk.",
		"............kNnk",
		".............kk.",
	},
	// Pi-hole: a shield that stops ads.
	"adblock": {
		"................",
		"..kkkkkkkkkkkk..",
		"..kssssssssssk..",
		"..ksssCCCCsssk..",
		"..kssCwwwCCssk..",
		"..ksCwwwCCwCsk..",
		"..ksCwwCCwwCsk..",
		"..ksCwCCwwwCsk..",
		"..kssCCwwCCssk..",
		"..ksssCCCCsssk..",
		"...kssssssssk...",
		"....kssssssk....",
		".....kssssk.....",
		"......kssk......",
		".......kk.......",
		"................",
	},
	// Traefik: a signpost.
	"router": {
		"................",
		".......kk.......",
		".kkkkkkkkkkkk...",
		".kyyyyyyyyyyyk..",
		".kyYYYYYYYYYyyk.",
		".kyyyyyyyyyyyk..",
		".kkkkkkNnkkkk...",
		"......kNnk......",
		"...kkkkNnkkkkkk.",
		"..kbbbbNnbbbbbk.",
		".kbBBBBNnBBBBbk.",
		"..kbbbbNnbbbbbk.",
		"...kkkkNnkkkkkk.",
		"......kNnk......",
		".....kkNnkk.....",
		"....kGGGGGGk....",
	},
	// OPNsense: a brick wall with a flame.
	"firewall": {
		"........k.......",
		".......kok......",
		"...k...koyk.....",
		"..kok.kooyyk....",
		"..koykoyyyyok...",
		"..kooooyywyyok..",
		"..kkkkkkkkkkkk..",
		"..kCCCgCCCgCCk..",
		"..kggggggggggk..",
		"..kCgCCCgCCCgk..",
		"..kggggggggggk..",
		"..kCCCgCCCgCCk..",
		"..kggggggggggk..",
		"..kCgCCCgCCCgk..",
		"..kkkkkkkkkkkk..",
		"................",
	},
	// Gitea: a tea cup with a branch on it.
	"git": {
		"................",
		".....k...k......",
		"....k...k.......",
		".....k...k......",
		"................",
		".kkkkkkkkkkk....",
		".kwwwwwwwwwkkk..",
		".kwSwwwSwwwk..k.",
		".kwSSwSwwwwk..k.",
		".kwSwSwwwwwkkk..",
		".kwSwwwwwwwk....",
		"..kwwwwwwwk.....",
		"...kkkkkkk......",
		"kGgggggggggGk...",
		".kkkkkkkkkkk....",
		"................",
	},
	// Arcane: a stack of shipping crates.
	"containers": {
		"................",
		"...kkkkkkkkkk...",
		"...kyYyYyYyYk...",
		"...kyYyYyYyYk...",
		"...kyYyYyYyYk...",
		"...kyYyYyYyYk...",
		"kkkkkkkkkkkkkkkk",
		"kbBbBbBkkcCcCcCk",
		"kbBbBbBkkcCcCcCk",
		"kbBbBbBkkcCcCcCk",
		"kbBbBbBkkcCcCcCk",
		"kbBbBbBkkcCcCcCk",
		"kbBbBbBkkcCcCcCk",
		"kkkkkkkkkkkkkkkk",
		"................",
		"................",
	},
	// Agent Vault: a safe with a dial.
	"vault": {
		"................",
		".kkkkkkkkkkkkkk.",
		".kGGGGGGGGGGGGk.",
		".kGkkkkkkkkkkGk.",
		".kGkggggggggkGk.",
		".kGkgggkkgggkGk.",
		".kGkggkyykggkGk.",
		".kGkggkyykggkGk.",
		".kGkgggkkgggkGk.",
		".kGkggggggggkGk.",
		".kGkggggggYYkGk.",
		".kGkggggggggkGk.",
		".kGkkkkkkkkkkGk.",
		".kGGGGGGGGGGGGk.",
		".kkkkkkkkkkkkkk.",
		"..kk........kk..",
	},
	// Cloudreve: a cloud holding a folder.
	"cloud-files": {
		"................",
		"................",
		"................",
		"................",
		"......kkkk......",
		".....kwwwwk.....",
		"..kkkwwwwwwkkk..",
		".kwwwwwwwwwwwwk.",
		"kwwwkkkwwwwwwwwk",
		"kwwwkYYkkkkkwwwk",
		"kwwwkyyyyyykwwwk",
		"kwwwkyyyyyykwwwk",
		"kbbbkkkkkkkkbbbk",
		".kkkkkkkkkkkkkk.",
		"................",
		"................",
	},
	// dosynch: a phone with an upload arrow.
	"phone-sync": {
		"................",
		".kkkkkkkk.......",
		".kGGGGGGk...k...",
		".kbbbbbbk..ksk..",
		".kbwbbbbk.kssSk.",
		".kbbbbbbkkssssSk",
		".kbbbbbbk.kksSkk",
		".kbbbbbbk..ksSk.",
		".kbbbbbbk..ksSk.",
		".kbbbbbbk..ksSk.",
		".kbbbbbbk..ksSk.",
		".kGGGGGGk..kkkk.",
		".kGGkkGGk.......",
		".kGGGGGGk.......",
		".kkkkkkkk.......",
		"................",
	},
	// doslim: a notebook and a pencil.
	"notebook": {
		"................",
		"................",
		".kkkkkkkkkk.kpk.",
		".kCwwwwwwwk.kGk.",
		".kCwwwwwwwk.kyk.",
		".kCwbbbbbwk.kyk.",
		".kCwwwwwwwk.kyk.",
		".kCwbbbbbwk.kyk.",
		".kCwwwwwwwk.kyk.",
		".kCwbbbbbwk.kyk.",
		".kCwwwwwwwk.kyk.",
		".kCwbbbwwwk.kwk.",
		".kCwwwwwwwk..k..",
		".kkkkkkkkkk.....",
		"................",
		"................",
	},
	// NetBird: a bird.
	"bird": {
		"................",
		"................",
		"........kkk.....",
		".......kbbbk....",
		".......kbkbkyyk.",
		"..kk...kbbbbkk..",
		"..kbkkkbbbbbk...",
		"...kbbbBBbbbk...",
		"...kbbBBBBbbk...",
		"....kbbBBbbk....",
		".....kbbbbk.....",
		"......kkkk......",
		".......Y.Y......",
		"................",
		"................",
		"................",
	},
	// Cloudflare and anything about DNS: a globe.
	"globe": {
		"................",
		".....kkkkkk.....",
		"...kkbbbbbbkk...",
		"..kbbsssbbbbbk..",
		".kbbssssbbbsbbk.",
		".kbsssssbbsssbk.",
		"kbbsssbbbsssssbk",
		"kbbbssbbbbssssbk",
		"kbbbbsbbbbbssbbk",
		"kbbbbbbsssbsbbbk",
		".kbbbbbssssbbbk.",
		".kbbbbbbsssbbbk.",
		"..kbbbbbbsbbbk..",
		"...kkbbbbbbkk...",
		".....kkkkkk.....",
		"................",
	},
	"database": {
		"................",
		"....kkkkkkkk....",
		"..kkbbbbbbbbkk..",
		"..kBBbbbbbbBBk..",
		"..kbBBBBBBBBbk..",
		"..kbbbbbbbbbbk..",
		"..kBBbbbbbbBBk..",
		"..kbBBBBBBBBbk..",
		"..kbbbbbbbbbbk..",
		"..kBBbbbbbbBBk..",
		"..kbBBBBBBBBbk..",
		"..kbbbbbbbbbbk..",
		"..kkbbbbbbbbkk..",
		"....kkkkkkkk....",
		"................",
		"................",
	},
	"photos": {
		"................",
		"................",
		".kkkkkkkkkkkkkk.",
		".kbbbbbbbbbyybk.",
		".kbbbbbbbbbyybk.",
		".kbbbbbbbbbbbbk.",
		".kbbbbkbbbbbbbk.",
		".kbbbkskbbbbbbk.",
		".kbbkssskbkbbbk.",
		".kbkssssskskbbk.",
		".kksssssssssskk.",
		".kSSSSSSSSSSSSk.",
		".kkkkkkkkkkkkkk.",
		"................",
		"................",
		"................",
	},
	"chart": {
		"................",
		".k..............",
		".k.........kkk..",
		".k.........kCk..",
		".k.........kCk..",
		".k......kkkkCk..",
		".k......kykkCk..",
		".k......kykkCk..",
		".k......kykkCk..",
		".k.kkk..kykkCk..",
		".k.ksk..kykkCk..",
		".k.ksk..kykkCk..",
		".k.ksk..kykkCk..",
		".kkkkkkkkkkkkkk.",
		"................",
		"................",
	},
	"lock": {
		"................",
		".....kkkkkk.....",
		"....kGGGGGGk....",
		"....kGkkkkGk....",
		"....kGk..kGk....",
		"....kGk..kGk....",
		"..kkkkkkkkkkkk..",
		"..kyyyyyyyyyyk..",
		"..kyyyykkyyyyk..",
		"..kyyyykkyyyyk..",
		"..kyyyyykyyyyk..",
		"..kyyyyyyyyyyk..",
		"..kYYYYYYYYYYk..",
		"..kkkkkkkkkkkk..",
		"................",
		"................",
	},
	// Anything else: a cardboard box.
	"box": {
		"................",
		"................",
		"..kkkkkkkkkkkk..",
		"..knnnnyynnnnk..",
		"..knnnnyynnnnk..",
		"..kkkkkkkkkkkk..",
		"..knnnnnnnnnNk..",
		"..knnnnnnnnnNk..",
		"..knnnnnnnnnNk..",
		"..knnnnnnnnnNk..",
		"..knnnnnnnnnNk..",
		"..knnnnnnnnnNk..",
		"..kNNNNNNNNNNk..",
		"..kkkkkkkkkkkk..",
		"................",
		"................",
	},
}

// iconAliases picks an icon from a service or container name when services.yml names none.
var iconAliases = []struct{ word, icon string }{
	{"pihole", "adblock"}, {"pi-hole", "adblock"}, {"adguard", "adblock"},
	{"traefik", "router"}, {"proxy", "router"},
	{"opnsense", "firewall"}, {"firewall", "firewall"}, {"router", "firewall"},
	{"gitea", "git"}, {"git", "git"},
	{"arcane", "containers"}, {"portainer", "containers"}, {"docker", "containers"},
	{"vault", "vault"}, {"secret", "vault"},
	{"cloudreve", "cloud-files"}, {"drive", "cloud-files"}, {"files", "cloud-files"},
	{"sync", "phone-sync"}, {"phone", "phone-sync"},
	{"doslim", "notebook"}, {"doquiz", "notebook"}, {"homework", "notebook"},
	{"netbird", "bird"}, {"vpn", "bird"},
	{"cloudflare", "globe"}, {"dns", "globe"},
	{"db", "database"}, {"postgres", "database"},
	{"photo", "photos"}, {"immich", "photos"},
	{"inspect", "inspect"}, {"grafana", "chart"}, {"metrics", "chart"},
}

// IconKeys lists the icon keys, sorted.
func IconKeys() []string {
	out := make([]string, 0, len(iconGrids))
	for k := range iconGrids {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pickIcon is the icon key for a service: its icon: when that names one, else the first alias
// found in its name or container, else "box".
func pickIcon(key, name, container string) string {
	if _, ok := iconGrids[key]; ok {
		return key
	}
	hay := strings.ToLower(name + " " + container)
	for _, a := range iconAliases {
		if strings.Contains(hay, a.word) {
			return a.icon
		}
	}
	return "box"
}

var iconCache = map[string]template.HTML{}

func init() {
	for k, g := range iconGrids {
		iconCache[k] = renderIcon(g, "")
	}
}

// renderIcon draws a grid as an SVG of squares, merging runs of one colour in a row into one
// rectangle. An empty title marks the icon as decoration.
func renderIcon(g [16]string, title string) template.HTML {
	var sb strings.Builder
	sb.WriteString(`<svg class="px" viewBox="0 0 16 16" shape-rendering="crispEdges" xmlns="http://www.w3.org/2000/svg"`)
	if title == "" {
		sb.WriteString(` aria-hidden="true">`)
	} else {
		fmt.Fprintf(&sb, ` role="img" aria-label="%s">`, template.HTMLEscapeString(title))
	}
	for y, row := range g {
		for x := 0; x < len(row); {
			c := row[x]
			j := x + 1
			for j < len(row) && row[j] == c {
				j++
			}
			if col, ok := iconPalette[c]; ok {
				fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="%d" height="1" fill="%s"/>`, x, y, j-x, col)
			}
			x = j
		}
	}
	sb.WriteString(`</svg>`)
	return template.HTML(sb.String())
}

// icon is the cached SVG of an icon key, for the templates.
func icon(key string) template.HTML {
	if h, ok := iconCache[key]; ok {
		return h
	}
	return iconCache["box"]
}

// IconSVG is a whole SVG file of an icon, for static/icon.svg.
func IconSVG(key string) string {
	return string(renderIcon(iconGrids[key], "")) + "\n"
}
