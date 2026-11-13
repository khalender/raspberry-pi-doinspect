package web

import (
	"encoding/xml"
	"os"
	"strings"
	"testing"
)

// TestIcons: every grid is 16 x 16 with known colours, renders as valid SVG, and the picker finds
// the right icon by key, by name and by container, else the box.
func TestIcons(t *testing.T) {
	for k, g := range iconGrids {
		for y, row := range g {
			if len(row) != 16 {
				t.Errorf("%s row %d has %d cells", k, y, len(row))
			}
			for i := 0; i < len(row); i++ {
				if _, ok := iconPalette[row[i]]; !ok && row[i] != '.' {
					t.Errorf("%s row %d: unknown colour %q", k, y, row[i])
				}
			}
		}
		if err := xml.Unmarshal([]byte(icon(k)), new(struct{})); err != nil {
			t.Errorf("%s: not valid SVG: %v", k, err)
		}
	}
	for _, tc := range []struct{ key, name, ctr, want string }{
		{"vault", "Whatever", "", "vault"},
		{"nope", "Pi-hole", "", "adblock"},
		{"", "Traefik", "traefik", "router"},
		{"", "OPNsense", "", "firewall"},
		{"", "Gitea", "gitea", "git"},
		{"", "Agent Vault", "agent-vault", "vault"},
		{"", "dosynch", "dosynch-server", "phone-sync"},
		{"", "Cloudflare", "", "globe"},
		{"", "Something", "thing", "box"},
	} {
		if got := pickIcon(tc.key, tc.name, tc.ctr); got != tc.want {
			t.Errorf("pickIcon(%q, %q, %q) = %q, want %q", tc.key, tc.name, tc.ctr, got, tc.want)
		}
	}
}

// TestIconFile keeps static/icon.svg equal to the inspect icon. UPDATE_ICON=1 writes it.
func TestIconFile(t *testing.T) {
	want := IconSVG("inspect")
	if os.Getenv("UPDATE_ICON") == "1" {
		if err := os.WriteFile("static/icon.svg", []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile("static/icon.svg")
	if err != nil || string(got) != want {
		t.Fatalf("static/icon.svg is not the inspect icon; run UPDATE_ICON=1 go test ./internal/web -run TestIconFile")
	}
	if !strings.Contains(want, `xmlns="http://www.w3.org/2000/svg"`) {
		t.Fatal("icon.svg needs the SVG namespace to load as a file")
	}
}
