//go:build linux || android

package sockat

// abstractNames: the kernel has abstract socket names, and the standard
// library reads a leading NUL or '@' as one (SPEC "短いパス", DR-0002).
const abstractNames = true
