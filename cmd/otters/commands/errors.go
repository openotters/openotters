package commands

import "github.com/openotters/openotters/pkg"

// unwrapRPC is a thin alias that re-exports pkg.UnwrapRPC under a
// package-local name so the many call sites in this package can stay
// terse. The real implementation lives in pkg so chatui can share it.
func unwrapRPC(err error) error { return pkg.UnwrapRPC(err) }
