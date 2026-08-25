package compendium

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type era struct {
	sets   []string
	before string
}

var formatEras = map[string]era{
	"premodern": {sets: []string{
		"4ed", "ice", "chr", "hml", "all", "mir", "vis", "5ed", "wth", "tmp",
		"sth", "exo", "usg", "ulg", "6ed", "uds", "mmq", "nem", "pcy", "inv",
		"pls", "7ed", "apc", "ody", "tor", "jud", "ons", "lgn", "scg",
	}},
	"predh": {before: "2011-06-17"},
}

func ApplyFormat(o Options, name string, era bool) (Options, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		if era {
			return Options{}, fmt.Errorf(
				"--era takes its bound from --format, so it needs one; the formats with "+
					"an era are %s — or name the sets yourself with --sets",
				strings.Join(eraFormats(), ", "))
		}
		return o, nil
	}
	if !slices.Contains(knownFormats, name) {
		return Options{}, fmt.Errorf("unknown format %q; want one of %s",
			name, strings.Join(knownFormats, ", "))
	}
	o.Legal = name

	if !era {
		return o, nil
	}
	e, ok := formatEras[name]
	if !ok {
		return Options{}, fmt.Errorf(
			"%s has no era here; hoard carries one for %s — drop --era to take every "+
				"printing %s allows, or name the sets yourself with --sets",
			name, strings.Join(eraFormats(), ", "), name)
	}
	if len(e.sets) > 0 && len(o.Sets) == 0 {
		o.Sets = slices.Clone(e.sets)
	}
	if e.before != "" && o.Before == "" {
		o.Before = e.before
	}
	return o, nil
}

func eraFormats() []string {
	return slices.Sorted(maps.Keys(formatEras))
}
