// Package receiverconfig contains receiver settings shared by command entry points.
package receiverconfig

// DefaultOutputDeviceForOS returns the virtual playback-device selector used
// when the user does not pass -device. Windows writes to VB-CABLE's playback
// endpoint; other platforms retain the established BlackHole default.
func DefaultOutputDeviceForOS(goos string) string {
	if goos == "windows" {
		return "cable"
	}
	return "blackhole"
}
