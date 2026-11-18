//go:build linux || android || illumos || solaris

package sockat

// abstractNames: the standard library reads a leading '@' as an abstract
// socket name here.
const abstractNames = true
