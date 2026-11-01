//go:build !linux

package raw

// statfs reports nothing outside Linux; doinspect runs on Linux, other systems only build and test.
func statfs(string) (Space, bool) { return Space{}, false }
