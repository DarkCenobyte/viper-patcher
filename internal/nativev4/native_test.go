package nativev4

import "testing"

func TestBLAKE3Vectors(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{name: "empty", want: "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262"},
		{name: "abc", input: []byte("abc"), want: "6437b3ac38465133ffb63b75273a8db548c558465d79db03fd359c6cd5bd9d85"},
		{name: "official-2049", input: officialVectorInput(2049), want: "5f4d72f40d7a5f82b15ca2b2e44b1de3c2ef86c426c95c1af0b6879522563030"},
		{name: "official-3072", input: officialVectorInput(3072), want: "b98cb0ff3623be03326b373de6b9095218513e64f1ee2edd2525c7ad1e5cffd2"},
		{name: "official-4096", input: officialVectorInput(4096), want: "015094013f57a5277b59d8475c0501042c0b642e531b0a1c8f58d2163229e969"},
		{name: "official-5121", input: officialVectorInput(5121), want: "628bd2cb2004694adaab7bbd778a25df25c47b9d4155a55f8fbd79f2fe154cff"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := HashBytes(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Hex() != test.want {
				t.Fatalf("BLAKE3 vector = %s, want %s", got.Hex(), test.want)
			}
		})
	}
}

func officialVectorInput(length int) []byte {
	result := make([]byte, length)
	for index := range result {
		result[index] = byte(index % 251)
	}
	return result
}
