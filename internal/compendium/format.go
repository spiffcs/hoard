package compendium

import (
	"fmt"
	"slices"
	"strings"
)

type formatPreset struct {
	legal string
	sets  []string
}

var formatPresets = map[string]formatPreset{
	"premodern": {
		legal: "premodern",
		sets: []string{
			"4ed", "ice", "chr", "hml", "all", "mir", "vis", "5ed", "wth", "tmp",
			"sth", "exo", "usg", "ulg", "6ed", "uds", "mmq", "nem", "pcy", "inv",
			"pls", "7ed", "apc", "ody", "tor", "jud", "ons", "lgn", "scg",
		},
	},
}

func ApplyFormat(o Options, name string) (Options, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return o, nil
	}
	preset, ok := formatPresets[name]
	if !ok {
		return Options{}, fmt.Errorf("unknown format %q; want one of %s",
			name, strings.Join(formatNames(), ", "))
	}
	if o.Legal == "" {
		o.Legal = preset.legal
	}
	if len(o.Sets) == 0 {
		o.Sets = slices.Clone(preset.sets)
	}
	return o, nil
}

func formatNames() []string {
	names := make([]string, 0, len(formatPresets))
	for name := range formatPresets {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
