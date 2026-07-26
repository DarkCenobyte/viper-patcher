package creatorgui

import "testing"

func TestIsUltraCompressionLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		selected string
		want     bool
	}{
		{selected: "", want: false},
		{selected: "19", want: false},
		{selected: "20", want: true},
		{selected: "21", want: true},
		{selected: "22", want: true},
		{selected: "23", want: false},
		{selected: "invalid", want: false},
	}

	for _, test := range tests {
		if got := isUltraCompressionLevel(test.selected); got != test.want {
			t.Fatalf("isUltraCompressionLevel(%q) = %v, want %v", test.selected, got, test.want)
		}
	}
}
