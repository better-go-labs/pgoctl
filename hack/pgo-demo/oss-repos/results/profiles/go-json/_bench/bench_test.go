package bench_test

import (
	"testing"

	gojson "github.com/goccy/go-json"
)

type Record struct {
	Name   string            `json:"name"`
	Value  int               `json:"value"`
	Tags   []string          `json:"tags"`
	Extra  map[string]string `json:"extra"`
	Nested struct {
		Score float64 `json:"score"`
		Label string  `json:"label"`
	} `json:"nested"`
}

var records []Record

func init() {
	for i := 0; i < 100; i++ {
		records = append(records, Record{
			Name:  "item",
			Value: i,
			Tags:  []string{"alpha", "beta", "gamma"},
			Extra: map[string]string{"k1": "v1", "k2": "v2"},
			Nested: struct {
				Score float64 `json:"score"`
				Label string  `json:"label"`
			}{float64(i) * 1.5, "label"},
		})
	}
}

func BenchmarkGoJSON(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, rec := range records {
			buf, _ := gojson.Marshal(rec)
			var out Record
			gojson.Unmarshal(buf, &out) //nolint:errcheck
		}
	}
}
