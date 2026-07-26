package creatorgui

import "testing"

func TestCompressionWarningLevelFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		selected string
		want     compressionWarningLevel
	}{
		{selected: "", want: compressionWarningNone},
		{selected: "9", want: compressionWarningNone},
		{selected: "10", want: compressionWarningElevated},
		{selected: "15", want: compressionWarningElevated},
		{selected: "19", want: compressionWarningElevated},
		{selected: "20", want: compressionWarningUltra},
		{selected: "21", want: compressionWarningUltra},
		{selected: "22", want: compressionWarningUltra},
		{selected: "23", want: compressionWarningNone},
		{selected: "invalid", want: compressionWarningNone},
	}

	for _, test := range tests {
		test := test
		t.Run(test.selected, func(t *testing.T) {
			t.Parallel()
			if got := compressionWarningLevelFor(test.selected); got != test.want {
				t.Fatalf("compressionWarningLevelFor(%q) = %v, want %v", test.selected, got, test.want)
			}
		})
	}
}
