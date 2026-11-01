package raw

import "syscall"

func statfs(path string) (Space, bool) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return Space{}, false
	}
	return Space{Size: float64(s.Blocks) * float64(s.Bsize), Avail: float64(s.Bavail) * float64(s.Bsize)}, true
}
