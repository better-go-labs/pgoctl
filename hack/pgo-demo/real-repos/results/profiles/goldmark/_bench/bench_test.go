package bench_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var markdownDoc = []byte(strings.Repeat(
	"# Heading\n\nParagraph with **bold**, *italic*, and `code` inline.\n\n"+
		"- list item one\n- list item two\n- list item three\n\n"+
		"```go\nfunc main() {\n\tfmt.Println(\"hello, pgo\")\n}\n```\n\n"+
		"> blockquote text here\n\n",
	200,
))

func BenchmarkGoldmark(b *testing.B) {
	engine := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		if err := engine.Convert(markdownDoc, &buf); err != nil {
			b.Fatal(err)
		}
	}
}
