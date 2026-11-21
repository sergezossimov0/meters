package sign

import (
	"strings"
	"testing"
)

// RFC 4231, test case 2.
const (
	rfcData = "what do ya want for nothing?"
	rfcSum  = "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
)

var rfcKey = []byte("Jefe")

func TestSum(t *testing.T) {
	if got := Sum([]byte(rfcData), rfcKey); got != rfcSum {
		t.Errorf("Sum = %s, want %s", got, rfcSum)
	}
}

func TestVerify(t *testing.T) {
	body := []byte(rfcData)
	tests := []struct {
		name string
		sig  string
		want bool
	}{
		{"valid", rfcSum, true},
		{"upper-case hex", strings.ToUpper(rfcSum), true},
		{"one digit off", rfcSum[:63] + "4", false},
		{"truncated", rfcSum[:62], false},
		{"not hex", strings.Repeat("z", 64), false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		if got := Verify(body, rfcKey, tt.sig); got != tt.want {
			t.Errorf("%s: Verify = %v, want %v", tt.name, got, tt.want)
		}
	}
	if Verify([]byte(rfcData+"!"), rfcKey, rfcSum) {
		t.Error("signature of another body must not verify")
	}
	if Verify(body, []byte("other"), rfcSum) {
		t.Error("signature under another key must not verify")
	}
}
