package audiodevice

import "strings"

// virtualMarkers 是各家虚拟声卡在设备名里留下的标志词，都已经过 NormalizeName。
//
// 判断"这是不是虚拟线"只能靠名字：驱动不提供这个属性，而名字正是用户在系统
// 声音设置里看到的那一份。宁可漏判也不能乱判 —— 标错了会把人引到错误设备上。
var virtualMarkers = []string{"vb-audio", "cable", "voicemeeter", "blackhole", "virtual"}

// IsVirtualCable 判断这个设备名是不是一条虚拟声卡（VB-CABLE、Voicemeeter、
// BlackHole 之类）。
//
// 接收端的两条音频路径都要落在虚拟线上：输出写进虚拟麦克风，回传采第二条虚拟
// 线。挑成物理设备不会报错，只会"对方听不见"或"把整台机器的声音送进会议"，
// 所以设置页需要把它标出来。
func IsVirtualCable(name string) bool {
	normalized := NormalizeName(name)
	if normalized == "" {
		return false
	}
	for _, marker := range virtualMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
