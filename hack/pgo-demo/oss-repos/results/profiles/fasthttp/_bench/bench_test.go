package bench_test

import (
	"bufio"
	"bytes"
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
	rd := bytes.NewReader(rawRequest)
	br := bufio.NewReader(rd)

	// Pre-check: verify parsing works
	rd.Reset(rawRequest)
	br.Reset(rd)
	if err := req.Read(br); err != nil {
		b.Fatalf("parsing failed: %v", err)
	}
	if len(req.Header.RequestURI()) == 0 {
		b.Fatal("parsing produced empty RequestURI")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rd.Reset(rawRequest)
		br.Reset(rd)
		req.Reset()
		if err := req.Read(br); err != nil {
			b.Fatal(err)
		}
	}
}
