package bench_test

import (
	"strings"
	"testing"

	"github.com/golang/snappy"
)

var compressData = []byte(strings.Repeat(
	"The quick brown fox jumps over the lazy dog. "+
		"Pack my box with five dozen liquor jugs. "+
		"How vexingly quick daft zebras jump! 0123456789. ",
	500,
))

func BenchmarkSnappy(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		compressed := snappy.Encode(nil, compressData)
		if _, err := snappy.Decode(nil, compressed); err != nil {
			b.Fatal(err)
		}
	}
}
