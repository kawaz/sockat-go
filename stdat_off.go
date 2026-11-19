//go:build !(linux || android || illumos || solaris)

package sockat

// stdReadsAtAsAbstract: the standard library hands a leading '@' to the
// kernel as it is here.
const stdReadsAtAsAbstract = false
