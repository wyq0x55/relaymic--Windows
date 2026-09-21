package rtc

import (
	"strings"
	"testing"
)

func sdpWith(sections ...string) string {
	return "v=0\r\n" + strings.Join(sections, "")
}

func TestOfferHasReceiveOnlyAudio(t *testing.T) {
	tests := []struct {
		name string
		sdp  string
		want bool
	}{
		{
			name: "显式的接收通道",
			sdp:  sdpWith("m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:1\r\na=recvonly\r\n"),
			want: true,
		},
		{
			name: "旧页面：只推流",
			sdp:  sdpWith("m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:0\r\na=sendonly\r\n"),
			want: false,
		},
		{
			name: "sendrecv 不算：那条 m-line 的接收方向和发送挤在一起",
			sdp:  sdpWith("m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:0\r\na=sendrecv\r\n"),
			want: false,
		},
		{
			name: "双向页面：第一条只发、第二条只收",
			sdp: sdpWith(
				"m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:0\r\na=sendonly\r\n",
				"m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:1\r\na=recvonly\r\n",
			),
			want: true,
		},
		{
			name: "视频的 recvonly 不能算到音频头上",
			sdp: sdpWith(
				"m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:0\r\na=sendonly\r\n",
				"m=video 9 UDP/TLS/RTP/SAVPF 96\r\na=mid:1\r\na=recvonly\r\n",
			),
			want: false,
		},
		{
			name: "没有方向行",
			sdp:  sdpWith("m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:0\r\n"),
			want: false,
		},
		{
			name: "空 SDP",
			sdp:  "",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := offerHasReceiveOnlyAudio(tt.sdp); got != tt.want {
				t.Fatalf("offerHasReceiveOnlyAudio() = %v, want %v\n%s", got, tt.want, tt.sdp)
			}
		})
	}
}
