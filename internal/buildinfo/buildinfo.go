// Package buildinfo carries the identity hoard presents to the services it
// talks to.
package buildinfo

// UserAgent identifies this tool on every outbound request. Scryfall requires
// a descriptive User-Agent header; one constant here means a version bump is
// one edit rather than a hunt through every client package.
const UserAgent = "hoard/0.1"
