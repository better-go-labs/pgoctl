package bench_test

import (
	"testing"

	"github.com/tidwall/gjson"
)

const jsonDoc = `{"store":{"book":[{"category":"reference","author":"Nigel Rees","title":"Sayings of the Century","price":8.95},{"category":"fiction","author":"Evelyn Waugh","title":"Sword of Honour","price":12.99},{"category":"fiction","author":"Herman Melville","title":"Moby Dick","price":8.99},{"category":"fiction","author":"J. R. R. Tolkien","title":"The Lord of the Rings","price":22.99}],"bicycle":{"color":"red","price":19.95}},"expensive":10}`

func BenchmarkGJSON(b *testing.B) {
	queries := []string{
		"store.book.#.author",
		"store.book.#[price<10].title",
		"store.bicycle.color",
		"store.book.#.price",
		"store.book.0.author",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, q := range queries {
			gjson.Get(jsonDoc, q)
		}
	}
}
