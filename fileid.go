package sockat

import "golang.org/x/sys/unix"

// fileID is (st_dev, st_ino).
type fileID struct{ dev, ino uint64 }

func statID(path string) (fileID, bool) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return fileID{}, false
	}
	return fileID{uint64(st.Dev), uint64(st.Ino)}, true
}
