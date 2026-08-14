package buildinfo

import "runtime/debug"

var Version = ""

var (
	GitCommit = "unknown"

	BuildDate = "unknown"
)

func Resolve() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
		var rev, dirty string
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "-dirty"
				}
			}
		}
		if rev != "" {
			if len(rev) > 12 {
				rev = rev[:12]
			}
			return "dev-" + rev + dirty
		}
	}
	return "dev"
}

var UserAgent = "hoard/" + Resolve() + " (+https://github.com/spiffcs/hoard)"

const FanContentNotice = "hoard is unofficial Fan Content permitted under the Fan Content Policy. " +
	"Not approved/endorsed by Wizards. Portions of the materials used are property of " +
	"Wizards of the Coast. ©Wizards of the Coast LLC."

const DataCredit = "Card data courtesy of Scryfall (https://scryfall.com), MTGJSON and TCGCSV. " +
	"Prices are estimates with absolutely no guarantee; see stores for final prices."
