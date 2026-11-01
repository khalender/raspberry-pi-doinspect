package raw

import (
	"regexp"
	"strings"
)

// Devices, interfaces and mounts that add noise without telling anything about load: loop and RAM
// disks, the host's VM taps and Docker's per-container links and bridges, and pseudo file systems.
var (
	skipDisk  = regexp.MustCompile(`^(loop|ram|sr|fd)\d*`)
	skipNet   = regexp.MustCompile(`^(lo|veth.*|vnet\d+|tap.*|br-[0-9a-f]{12}|docker0|pflog\d*|pfsync\d*|enc\d*)$`)
	skipFSTyp = map[string]bool{"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true, "ramfs": true, "nsfs": true,
		"autofs": true, "devfs": true, "fdescfs": true, "procfs": true, "linprocfs": true, "linsysfs": true, "nullfs": true, "efivarfs": true}
)

func keepDisk(dev string) bool { return !skipDisk.MatchString(dev) }
func keepNet(dev string) bool  { return !skipNet.MatchString(dev) }

func keepFS(mount, fstype string) bool {
	if skipFSTyp[fstype] {
		return false
	}
	for _, p := range []string{"/run", "/sys", "/proc", "/dev", "/var/lib/docker", "/snap", "/boot/efi"} {
		if mount == p || strings.HasPrefix(mount, p+"/") {
			return false
		}
	}
	return true
}
