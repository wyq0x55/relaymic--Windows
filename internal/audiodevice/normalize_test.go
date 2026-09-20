package audiodevice

import "testing"

func TestNormalizeName(t *testing.T) {
	if got, want := NormalizeName(" CABLE Input "), "cableinput"; got != want {
		t.Fatalf("NormalizeName returned %q, want %q", got, want)
	}
}
