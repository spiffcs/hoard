// Package buildinfo carries the identity hoard presents to the services it
// talks to and to its own users.
package buildinfo

import "runtime/debug"

// Version is the release version, stamped by the release pipeline via
// -ldflags "-X github.com/spiffcs/hoard/internal/buildinfo.Version=v1.2.3".
// Source builds leave it empty and Resolve falls back to what the Go module
// system knows about the build.
var Version = ""

// GitCommit and BuildDate complete the release identity Version starts:
// stamped by the same goreleaser ldflags block. Source builds keep "unknown" —
// Resolve's VCS fallback already folds the revision into the version string,
// so these stay dumb rather than duplicating that logic.
var (
	// GitCommit is the commit the binary was built from.
	GitCommit = "unknown"
	// BuildDate is an RFC3339 timestamp of the release build.
	BuildDate = "unknown"
)

// Resolve reports the best version string this binary can know about itself:
// the stamped release version, else the module version `go install` recorded,
// else the VCS revision of a source build.
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

// UserAgent identifies this tool on every outbound request. Scryfall requires
// a descriptive User-Agent header, and the contact URL is how a provider
// reaches us about traffic before reaching for a block instead.
var UserAgent = "hoard/" + Resolve() + " (+https://github.com/spiffcs/hoard)"

// FanContentNotice is the verbatim text Wizards of the Coast's Fan Content
// Policy requires every piece of fan content to carry. It is what permits a
// tool built on Magic card data and imagery to exist at all — do not reword
// it (docs/data-licensing.md §7).
const FanContentNotice = "hoard is unofficial Fan Content permitted under the Fan Content Policy. " +
	"Not approved/endorsed by Wizards. Portions of the materials used are property of " +
	"Wizards of the Coast. ©Wizards of the Coast LLC."

// DataCredit acknowledges the data sources and carries Scryfall's price
// caveat, which their guidelines ask price consumers to surface.
const DataCredit = "Card data courtesy of Scryfall (https://scryfall.com), MTGJSON and TCGCSV. " +
	"Prices are estimates with absolutely no guarantee; see stores for final prices."
