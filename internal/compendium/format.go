package compendium

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type era struct {
	sets    []string
	before  string
	only    map[string][]string
	except  []string
	eraOnly bool
}

var formatEras = map[string]era{
	"premodern": {sets: []string{
		"4ed", "ice", "chr", "hml", "all", "mir", "vis", "5ed", "wth", "tmp",
		"sth", "exo", "usg", "ulg", "6ed", "uds", "mmq", "nem", "pcy", "inv",
		"pls", "7ed", "apc", "ody", "tor", "jud", "ons", "lgn", "scg",
	}},
	"predh": {before: "2011-06-17"},
	"aaa": {
		sets: []string{
			"lea", "leb", "2ed", "3ed", "4ed", "arn", "atq", "leg",
			"drk", "fem", "hml", "ice", "all",
		},
		only: map[string][]string{"apc": {
			"Battlefield Forge", "Caves of Koilos", "Llanowar Wastes",
			"Shivan Reef", "Yavimaya Coast",
		}},
		except:  []string{"Mind Twist"},
		eraOnly: true,
	},
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

	e, ok := formatEras[name]
	if !e.eraOnly {
		o.Legal = name
	}

	if !era {
		if e.eraOnly {
			return Options{}, fmt.Errorf(
				"%s is not a legality Scryfall records, so hoard can only build it from "+
					"its era; pass --era with it, or name the sets yourself with --sets",
				name)
		}
		return o, nil
	}
	if !ok {
		return Options{}, fmt.Errorf(
			"%s has no era here; hoard carries one for %s — drop --era to take every "+
				"printing %s allows, or name the sets yourself with --sets",
			name, strings.Join(eraFormats(), ", "), name)
	}
	if len(e.sets) > 0 && len(o.Sets) == 0 {
		o.Sets = slices.Clone(e.sets)
		if len(e.only) > 0 && len(o.Only) == 0 {
			o.Only = map[string][]string{}
			for set, names := range e.only {
				o.Only[set] = slices.Clone(names)
			}
		}
	}
	if e.before != "" && o.Before == "" {
		o.Before = e.before
	}
	if len(e.except) > 0 && len(o.Except) == 0 {
		o.Except = slices.Clone(e.except)
	}
	return o, nil
}

func eraFormats() []string {
	return slices.Sorted(maps.Keys(formatEras))
}
