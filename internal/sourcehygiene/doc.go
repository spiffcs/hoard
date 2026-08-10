// Package sourcehygiene holds repo-wide checks over hoard's own Go sources —
// properties that belong to no single package because they are about how the
// code is written rather than what it does.
//
// It has no runtime code and nothing imports it. Everything here is a test.
package sourcehygiene
