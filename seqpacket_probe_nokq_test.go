//go:build linux || illumos

package sockat_test

// kqueueReadable is for the systems with kqueue.
func kqueueReadable(int) string { return "" }
