package main

import "testing"

func TestDefaultOutputDeviceForOS(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{goos: "windows", want: "cable"},
		{goos: "darwin", want: "blackhole"},
		{goos: "linux", want: "blackhole"},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			if got := defaultOutputDeviceForOS(tt.goos); got != tt.want {
				t.Fatalf("defaultOutputDeviceForOS(%q) = %q, want %q", tt.goos, got, tt.want)
			}
		})
	}
}
