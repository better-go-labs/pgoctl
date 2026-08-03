// Command loadgen hammers a Prometheus server with remote-write traffic so a
// concurrent pprof capture exercises tsdb hot paths: head append, WAL writes,
// and series churn.
package main

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
)

type series struct {
	labels []prompb.Label // last label is the churnable "pod" label
}

func main() {
	url := flag.String("url", "http://localhost:9090/api/v1/write", "remote-write endpoint")
	duration := flag.Int("duration", 0, "run duration in seconds (0 = run until SIGINT/SIGTERM)")
	numSeries := flag.Int("series", 4000, "number of concurrent series")
	batch := flag.Int("batch", 500, "max samples per WriteRequest")
	interval := flag.Int("interval", 1, "flush interval in seconds")
	churn := flag.Float64("churn", 0.1, "fraction of series churned per interval")
	job := flag.String("job", "loadgen", "value of the job label")
	flag.Parse()

	if *numSeries < 1 {
		*numSeries = 1
	}
	if *batch < 1 {
		*batch = 1
	}
	*churn = math.Max(0, math.Min(1, *churn))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*duration)*time.Second)
		defer cancel()
	}

	series := make([]series, *numSeries)
	for i := range series {
		name := "loadgen_metric_a"
		if i%2 == 1 {
			name = "loadgen_metric_b"
		}
		series[i].labels = []prompb.Label{
			{Name: "__name__", Value: name},
			{Name: "job", Value: *job},
			{Name: "instance", Value: "host-" + strconv.Itoa(i)},
			{Name: "pod", Value: newPod()},
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	var samples, requests, errors int64
	lastLog := time.Now()
	ticker := time.NewTicker(time.Duration(*interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("loadgen shutdown: samples=%d requests=%d errors=%d\n", samples, requests, errors)
			return
		case <-ticker.C:
			// Drain coalesced ticks so a slow flush doesn't backlog work.
			select {
			case <-ticker.C:
			default:
			}
		}

		now := time.Now()
		churnSeries(series, *churn)

		var req prompb.WriteRequest
		for i := range series {
			req.Timeseries = append(req.Timeseries, prompb.TimeSeries{
				Labels: series[i].labels,
				Samples: []prompb.Sample{{
					Value:     float64(now.Unix()) + math.Sin(float64(i))*0.5 + rand.Float64()*0.1,
					Timestamp: now.UnixMilli(),
				}},
			})
			if len(req.Timeseries) >= *batch {
				send(client, *url, &req, &samples, &requests, &errors)
				req.Timeseries = req.Timeseries[:0]
			}
		}
		if len(req.Timeseries) > 0 {
			send(client, *url, &req, &samples, &requests, &errors)
		}

		if time.Since(lastLog) >= 5*time.Second {
			log.Printf("samples=%d requests=%d errors=%d", samples, requests, errors)
			lastLog = time.Now()
		}
	}
}

// send marshals, compresses, and POSTs one WriteRequest, updating counters.
// Failures are logged but never fatal: CI wants sustained load.
func send(client *http.Client, url string, req *prompb.WriteRequest, samples, requests, errors *int64) {
	data, err := proto.Marshal(req)
	if err != nil {
		log.Printf("marshal: %v", err)
		*errors++
		return
	}
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(snappy.Encode(nil, data)))
	if err != nil {
		log.Printf("new request: %v", err)
		*errors++
		return
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")
	httpReq.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	httpReq.Header.Set("User-Agent", "pgoctl-loadgen")

	resp, err := client.Do(httpReq)
	*requests++
	if err != nil {
		log.Printf("request: %v", err)
		*errors++
		return
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("non-2xx response: %s", resp.Status)
		*errors++
		return
	}
	*samples += int64(len(req.Timeseries))
}

// churnSeries replaces the pod label on a random subset of series, creating
// brand-new series identities that force new head chunks and WAL appends.
func churnSeries(series []series, frac float64) {
	n := int(math.Round(float64(len(series)) * frac))
	if n <= 0 {
		return
	}
	for _, i := range rand.Perm(len(series))[:n] {
		series[i].labels[3].Value = newPod()
	}
}

// newPod returns a pod-<8 hex chars> label value.
func newPod() string {
	var b [4]byte
	if _, err := crand.Read(b[:]); err != nil {
		return fmt.Sprintf("pod-%08x", rand.Uint32())
	}
	return "pod-" + hex.EncodeToString(b[:])
}
