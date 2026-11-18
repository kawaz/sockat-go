//go:build !(linux || android || illumos || solaris)

package sockat

// abstractNames: no abstract socket names here.
const abstractNames = false
