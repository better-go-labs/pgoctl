package bench_test

import (
	"testing"

	"github.com/valyala/fasthttp"
)

var rawRequest = []byte(
	"GET /api/v1/users?page=1&limit=100&sort=created_at HTTP/1.1\r\n" +
		"Host: api.example.com\r\n" +
		"Content-Type: application/json; charset=utf-8\r\n" +
		"Accept: application/json\r\n" +
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.test.signature\r\n" +
		"X-Request-ID: abc-123-def-456\r\n" +
		"User-Agent: bench/1.0\r\n" +
		"\r\n",
)

func BenchmarkFastHTTPParsing(b *testing.B) {
	var req fasthttp.Request
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req.Reset()
		req.Header.Parse(rawRequest)
		_ = string(req.Header.RequestURI())
		_ = string(req.Header.Method())
		_ = req.URI().QueryArgs().String()
	}
}
