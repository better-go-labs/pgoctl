// hashcalc-svc: CPU-bound service with interface-heavy dispatch.
// Designed to be a good PGO candidate: hot interface calls, deep stacks,
// inlineable leaves. Exposes /debug/pprof for profile collection.
package main

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"hash"
	"log"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Processor is the hot interface — dispatch here is what PGO can devirtualize.
type Processor interface {
	Process(data []byte) []byte
	Name() string
}

type hasherProc struct {
	name    string
	newHash func() hash.Hash
}

func (h *hasherProc) Process(data []byte) []byte {
	hh := h.newHash()
	hh.Write(data)
	return hh.Sum(nil)
}

func (h *hasherProc) Name() string { return h.name }

type base64Proc struct{}

func (b *base64Proc) Process(data []byte) []byte {
	out := make([]byte, base64.StdEncoding.EncodedLen(len(data)))
	base64.StdEncoding.Encode(out, data)
	return out
}
func (b *base64Proc) Name() string { return "base64" }

type reverseProc struct{}

func (r *reverseProc) Process(data []byte) []byte {
	out := make([]byte, len(data))
	for i, c := range data {
		out[len(data)-1-i] = c
	}
	return out
}
func (r *reverseProc) Name() string { return "reverse" }

type sortProc struct{}

func (s *sortProc) Process(data []byte) []byte {
	words := strings.Fields(string(data))
	sort.Strings(words)
	return []byte(strings.Join(words, " "))
}
func (s *sortProc) Name() string { return "sort" }

type chainProc struct {
	stages []Processor
}

func (c *chainProc) Process(data []byte) []byte {
	cur := data
	for _, p := range c.stages {
		cur = p.Process(cur)
	}
	return cur
}
func (c *chainProc) Name() string { return "chain" }

var processors []Processor

func init() {
	processors = []Processor{
		&hasherProc{"md5", md5.New},
		&hasherProc{"sha1", sha1.New},
		&hasherProc{"sha256", sha256.New},
		&hasherProc{"sha512", sha512.New},
		&base64Proc{},
		&reverseProc{},
		&sortProc{},
		&chainProc{stages: []Processor{
			&hasherProc{"sha256", sha256.New},
			&base64Proc{},
			&reverseProc{},
		}},
	}
}

// doWork is the hot path. Called in a tight loop per request.
// Iterates all processors — heavy on interface dispatch.
func doWork(data []byte, rounds int) map[string]string {
	results := make(map[string]string, len(processors))
	for i := 0; i < rounds; i++ {
		for _, p := range processors {
			out := p.Process(data)
			if i == rounds-1 {
				results[p.Name()] = base64.StdEncoding.EncodeToString(out)
			}
		}
	}
	return results
}

func handleProcess(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	data := q.Get("data")
	if data == "" {
		data = "the quick brown fox jumps over the lazy dog - benchmark payload for PGO demo"
	}
	rounds, _ := strconv.Atoi(q.Get("rounds"))
	if rounds < 1 {
		rounds = 10
	}

	results := doWork([]byte(data), rounds)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok\n")
}

func handleBench(w http.ResponseWriter, r *http.Request) {
	// Fixed heavy workload for benchmarking latency
	payload := strings.Repeat("abcdefghijklmnopqrstuvwxyz0123456789 ", 100)
	results := doWork([]byte(payload), 50)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results": results,
		"goroutines": runtime.NumGoroutine(),
	})
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	workers := flag.Int("workers", runtime.NumCPU(), "GOMAXPROCS")
	flag.Parse()

	runtime.GOMAXPROCS(*workers)

	mux := http.NewServeMux()
	mux.HandleFunc("/process", handleProcess)
	mux.HandleFunc("/bench", handleBench)
	mux.HandleFunc("/ping", handlePing)

	// pprof registered on DefaultServeMux via import
	go func() {
		log.Println("pprof on :6060")
		log.Fatal(http.ListenAndServe(":6060", nil))
	}()

	log.Printf("hashcalc-svc on %s (GOMAXPROCS=%d)", *addr, *workers)
	srv := &http.Server{
		Addr:    *addr,
		Handler: mux,
	}
	ctx := context.Background()
	_ = ctx
	log.Fatal(srv.ListenAndServe())
}
