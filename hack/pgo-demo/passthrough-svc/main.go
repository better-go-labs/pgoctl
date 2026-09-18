// passthrough-svc: I/O-bound service, expected to be a PGO non-candidate.
// Spends nearly all CPU time in net/http blocking, syscall read/write, and
// runtime scheduler. Profile will be flat with no hot inlineable functions.
// Exposes /debug/pprof for profile collection.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"time"
)

func handleEcho(w http.ResponseWriter, r *http.Request) {
	// Mostly I/O: read request body, write it back. No CPU work.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read error", 500)
		return
	}
	w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
	w.Write(body)
}

func handleSleep(w http.ResponseWriter, r *http.Request) {
	// Simulates I/O wait: goroutine blocks on timer (stands in for DB/network).
	time.Sleep(time.Millisecond)
	fmt.Fprintf(w, "ok\n")
}

func handleProxy(w http.ResponseWriter, r *http.Request) {
	// Proxy-style: read from a fixed endpoint and copy response.
	// In the benchmark we target ourselves (/echo) — the work is all I/O.
	resp, err := http.Get("http://localhost:8081/echo")
	if err != nil {
		http.Error(w, "upstream error", 502)
		return
	}
	defer resp.Body.Close()
	io.Copy(w, resp.Body)
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok\n")
}

func main() {
	addr := flag.String("addr", ":8081", "listen address")
	flag.Parse()

	runtime.GOMAXPROCS(runtime.NumCPU())

	mux := http.NewServeMux()
	mux.HandleFunc("/echo", handleEcho)
	mux.HandleFunc("/sleep", handleSleep)
	mux.HandleFunc("/ping", handlePing)

	go func() {
		log.Println("pprof on :6061")
		log.Fatal(http.ListenAndServe(":6061", nil))
	}()

	log.Printf("passthrough-svc on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
