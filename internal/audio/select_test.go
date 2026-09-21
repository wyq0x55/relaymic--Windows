package audio

import "testing"

func TestSelectDevice(t *testing.T) {
	devices := []Device{
		{Name: "スピーカー (Realtek(R) Audio)"},
		{Name: "CABLE Output (VB-Audio Virtual Cable)"},
		{Name: "CABLE-A Output (VB-Audio Virtual Cable)"},
		{Name: "マイク (Realtek(R) Audio)", IsDefault: true},
	}
	tests := []struct {
		name     string
		selector string
		want     string
		wantErr  bool
	}{
		{name: "unique substring", selector: "CABLE-A Output", want: "CABLE-A Output (VB-Audio Virtual Cable)"},
		{name: "case and space insensitive", selector: " cable-a  output ", want: "CABLE-A Output (VB-Audio Virtual Cable)"},
		{name: "ambiguous selector", selector: "cable", wantErr: true},
		{name: "no match", selector: "blackhole", wantErr: true},
		{name: "empty selector is not a wildcard", selector: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SelectDevice(devices, tt.selector)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("SelectDevice(%q) = %q, want error", tt.selector, got.Name)
				}
				return
			}
			if err != nil {
				t.Fatalf("SelectDevice(%q) error = %v", tt.selector, err)
			}
			if got.Name != tt.want {
				t.Fatalf("SelectDevice(%q) = %q, want %q", tt.selector, got.Name, tt.want)
			}
		})
	}
}

// 默认设备是系统属性，不是"选不到就随便挑一个"的许可。
// 回传路径尤其不能这样：挑错设备等于把整台机器的系统声音送进会议。
func TestSelectDeviceNeverFallsBackToTheDefault(t *testing.T) {
	devices := []Device{{Name: "マイク (Realtek(R) Audio)", IsDefault: true}}
	if got, err := SelectDevice(devices, "blackhole"); err == nil {
		t.Fatalf("SelectDevice() fell back to the default device %q", got.Name)
	}
}

func TestSelectDeviceOnEmptyList(t *testing.T) {
	if _, err := SelectDevice(nil, "cable"); err == nil {
		t.Fatal("SelectDevice() accepted an empty device list")
	}
}
