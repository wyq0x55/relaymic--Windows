package signaling

import "testing"

func TestNewPairingCodeHasSixDigits(t *testing.T) {
	seen := make(map[PairingCode]struct{}, 200)
	for i := 0; i < 200; i++ {
		code, err := NewPairingCode()
		if err != nil {
			t.Fatalf("NewPairingCode() error = %v", err)
		}
		if len(code) != PairingCodeDigits {
			t.Fatalf("NewPairingCode() = %q, want %d digits", code, PairingCodeDigits)
		}
		for _, r := range string(code) {
			if r < '0' || r > '9' {
				t.Fatalf("NewPairingCode() = %q, want decimal digits only", code)
			}
		}
		seen[code] = struct{}{}
	}
	// 200 次抽样落在 10^6 的码空间里，几乎不该重复；重复过多说明随机源或取模有问题。
	if len(seen) < 150 {
		t.Fatalf("200 次抽样只产生 %d 个不同配对码，随机性不足", len(seen))
	}
}

func TestNormalizePairingCode(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    PairingCode
		wantErr bool
	}{
		{name: "plain", in: "583921", want: "583921"},
		{name: "surrounding spaces", in: "  583921 ", want: "583921"},
		{name: "grouped by space", in: "583 921", want: "583921"},
		{name: "grouped by hyphen", in: "583-921", want: "583921"},
		{name: "too short", in: "58392", wantErr: true},
		{name: "too long", in: "5839210", wantErr: true},
		{name: "letters", in: "58392a", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "only separators", in: "- -", wantErr: true},
		{name: "full width digits", in: "５８３９２１", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePairingCode(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizePairingCode(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizePairingCode(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizePairingCode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEqualPairingCode(t *testing.T) {
	if !EqualPairingCode("583921", "583921") {
		t.Fatal("EqualPairingCode() = false for identical codes")
	}
	if EqualPairingCode("583921", "583922") {
		t.Fatal("EqualPairingCode() = true for different codes")
	}
	if EqualPairingCode("58391", "583921") {
		t.Fatal("EqualPairingCode() = true for different lengths")
	}
}
