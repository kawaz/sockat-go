//go:build !(linux || android)

package sockat

// abstractNames: the kernel has no abstract socket names here, so a leading
// NUL is a NUL like any other and a leading '@' an ordinary byte of a path.
const abstractNames = false
