package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Metrics struct {
	httpRequests     atomic.Uint64
	httpErrors       atomic.Uint64
	dbQueries        atomic.Uint64
	dbErrors         atomic.Uint64
	dbNanoseconds    atomic.Uint64
	mqPublishes      atomic.Uint64
	mqErrors         atomic.Uint64
	mqRetries        atomic.Uint64
	wsConnections    atomic.Int64
	consumerFailures atomic.Uint64
	mu               sync.RWMutex
	mqBacklog        map[string]int
}

func NewMetrics() *Metrics      { return &Metrics{mqBacklog: make(map[string]int)} }
func (m *Metrics) Path() string { return "/metrics" }
func (m *Metrics) ObserveHTTP(status int) {
	m.httpRequests.Add(1)
	if status >= 500 {
		m.httpErrors.Add(1)
	}
}
func (m *Metrics) ObserveDB(elapsed time.Duration, err error) {
	m.dbQueries.Add(1)
	m.dbNanoseconds.Add(uint64(elapsed))
	if err != nil {
		m.dbErrors.Add(1)
	}
}
func (m *Metrics) ObserveMQ(retries int, err error) {
	m.mqPublishes.Add(1)
	if retries > 0 {
		m.mqRetries.Add(uint64(retries))
	}
	if err != nil {
		m.mqErrors.Add(1)
	}
}
func (m *Metrics) AddWS(delta int64)   { m.wsConnections.Add(delta) }
func (m *Metrics) IncConsumerFailure() { m.consumerFailures.Add(1) }
func (m *Metrics) SetMQBacklog(queue string, count int) {
	m.mu.Lock()
	m.mqBacklog[queue] = count
	m.mu.Unlock()
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	seconds := float64(m.dbNanoseconds.Load()) / float64(time.Second)
	fmt.Fprintf(w, "my_im_http_requests_total %d\nmy_im_http_errors_total %d\n", m.httpRequests.Load(), m.httpErrors.Load())
	fmt.Fprintf(w, "my_im_db_queries_total %d\nmy_im_db_errors_total %d\nmy_im_db_query_seconds_sum %f\n", m.dbQueries.Load(), m.dbErrors.Load(), seconds)
	fmt.Fprintf(w, "my_im_mq_publishes_total %d\nmy_im_mq_publish_errors_total %d\nmy_im_mq_publish_retries_total %d\n", m.mqPublishes.Load(), m.mqErrors.Load(), m.mqRetries.Load())
	fmt.Fprintf(w, "my_im_ws_connections %d\nmy_im_consumer_failures_total %d\n", m.wsConnections.Load(), m.consumerFailures.Load())
	m.mu.RLock()
	queues := make([]string, 0, len(m.mqBacklog))
	for queue := range m.mqBacklog {
		queues = append(queues, queue)
	}
	sort.Strings(queues)
	for _, queue := range queues {
		fmt.Fprintf(w, "my_im_mq_backlog{queue=%q} %d\n", strings.ReplaceAll(queue, "\"", ""), m.mqBacklog[queue])
	}
	m.mu.RUnlock()
}
