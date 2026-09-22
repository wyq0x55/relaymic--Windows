package audiodevice

import "testing"

// 设置页要把"这条是虚拟线"标出来。选错设备在这里不是小事：输出挑到扬声器
// 就是"对方听不见"，回传挑到自己正在写的那条线就是自己采自己。
func TestIsVirtualCableRecognizesTheCablesPeopleActuallyInstall(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: "CABLE Input (VB-Audio Virtual Cable)", want: true},
		{name: "CABLE Output (VB-Audio Virtual Cable)", want: true},
		{name: "CABLE In 16ch (VB-Audio Virtual Cable)", want: true},
		{name: "VoiceMeeter Aux Output (VB-Audio VoiceMeeter AUX VAIO)", want: true},
		{name: "VoiceMeeter Input (VB-Audio VoiceMeeter VAIO)", want: true},
		{name: "BlackHole 2ch", want: true},
		{name: "Hi-Fi Cable Input (VB-Audio Hi-Fi Cable)", want: true},
		{name: "スピーカー (Realtek(R) Audio)", want: false},
		{name: "ヘッドホン (Realtek(R) Audio)", want: false},
		{name: "S24C36x (4- HD Audio Driver for Display Audio)", want: false},
		{name: "マイク (Realtek(R) Audio)", want: false},
		{name: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsVirtualCable(tc.name); got != tc.want {
				t.Fatalf("IsVirtualCable(%q) = %v，want %v", tc.name, got, tc.want)
			}
		})
	}
}
