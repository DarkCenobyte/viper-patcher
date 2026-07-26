package creatorgui

import "testing"

func TestFittedFilePairTableHeight(t *testing.T) {
	tests := []struct {
		name     string
		desired  float32
		maximum  float32
		expected float32
	}{
		{name: "already fits", desired: 900, maximum: 940, expected: filePairTablePreferredHeight},
		{name: "shrinks by overflow", desired: 1040, maximum: 940, expected: 170},
		{name: "clamps to minimum", desired: 1200, maximum: 940, expected: filePairTableMinimumHeight},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := fittedFilePairTableHeight(
				filePairTablePreferredHeight,
				filePairTableMinimumHeight,
				test.desired,
				test.maximum,
			)
			if actual != test.expected {
				t.Fatalf("height = %.0f, want %.0f", actual, test.expected)
			}
		})
	}
}
