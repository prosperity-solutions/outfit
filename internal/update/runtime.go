package update

import "runtime"

// Split out so tests can name the platform without build tags.
var (
	runtimeGOOS   = runtime.GOOS
	runtimeGOARCH = runtime.GOARCH
)
