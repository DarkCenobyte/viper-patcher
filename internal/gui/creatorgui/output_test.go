package creatorgui

import "testing"

func TestNormalizeOutputName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "adds extension", input: "update", want: "update.vipr"},
		{name: "keeps extension", input: " update.VIPR ", want: "update.VIPR"},
		{name: "rejects empty", input: "   ", wantErr: true},
		{name: "rejects extension only", input: ".vipr", wantErr: true},
		{name: "rejects directory", input: "sub/update.vipr", wantErr: true},
		{name: "rejects other extension", input: "update.zip", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := normalizeOutputName(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("normalizeOutputName(%q) succeeded with %q", test.input, actual)
				}
				return
			}
			if err != nil || actual != test.want {
				t.Fatalf("normalizeOutputName(%q) = %q, %v; want %q", test.input, actual, err, test.want)
			}
		})
	}
}
