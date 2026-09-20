package audiodevice

import "testing"

func TestUniqueMatchIndex(t *testing.T) {
	tests := []struct {
		name     string
		devices  []string
		selector string
		want     int
		wantErr  bool
	}{
		{name: "normalizes case and spaces", devices: []string{"CABLE Input (VB-Audio Virtual Cable)"}, selector: " cable input ", want: 0},
		{name: "rejects empty selector", devices: []string{"Speakers"}, selector: "", want: -1, wantErr: true},
		{name: "rejects no match", devices: []string{"Speakers"}, selector: "cable input", want: -1, wantErr: true},
		{name: "rejects ambiguity", devices: []string{"CABLE Input", "CABLE Input 2"}, selector: "cable input", want: -1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UniqueMatchIndex(tt.devices, tt.selector)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UniqueMatchIndex(%q, %q) error = %v, wantErr %v", tt.devices, tt.selector, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("UniqueMatchIndex(%q, %q) = %d, want %d", tt.devices, tt.selector, got, tt.want)
			}
		})
	}
}
