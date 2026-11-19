//go:build linux || android || illumos || solaris

package sockat

// stdReadsAtAsAbstract: the standard library (syscall.SockaddrUnix) turns a
// leading '@' into a NUL here, making the path an abstract name, whether or
// not the kernel has such names (illumos has none).
const stdReadsAtAsAbstract = true
