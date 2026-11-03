package requestlog

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const (
	defaultBufferSize = 10_000
	defaultBatchSize  = 100
	defaultFlushEvery = 5 * time.Second
)

// Sink stores one flushed batch. The writer reuses the slice for the next
// batch, so an implementation must be done with it by the time it returns.
type Sink interface {
	CreateBatch(ctx context.Context, rows []models.RequestLog) error
}

// Writer buffers request log rows and inserts them in batches.
//
// It is a foundation.Runner so that the framework's shutdown flushes it:
// AppServiceProvider hands it over through Runners, and Start calls Shutdown on
// SIGINT/SIGTERM. The framework shuts every runner down at once, with no
// order, so the HTTP server may still be finishing requests after this drain:
// once shut down, Enqueue writes each row itself instead of buffering it.
type Writer struct {
	sink Sink
	ch   chan models.RequestLog
	done chan struct{}
	wg   sync.WaitGroup
	once sync.Once
	// mu makes "shut down" and "row sent to ch" mutually exclusive, so every
	// row that reached ch is there before the drain starts.
	mu     sync.RWMutex
	closed bool
}

// NewWriter starts the background flush loop.
func NewWriter(sink Sink) *Writer {
	w := &Writer{
		sink: sink,
		ch:   make(chan models.RequestLog, defaultBufferSize),
		done: make(chan struct{}),
	}
	w.wg.Add(1)
	go w.run()
	return w
}

// Enqueue never blocks the request it logs: with the buffer full, the row is
// dropped with a warning rather than holding up the response. After Shutdown
// it writes the row at once, the only way a request finishing during the
// process's stop is logged.
func (w *Writer) Enqueue(row models.RequestLog) {
	row = storable(row)
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		w.write([]models.RequestLog{row})
		return
	}
	select {
	case w.ch <- row:
	default:
		facades.Log().Warning("request log writer: buffer full, dropping entry")
	}
}

const pathColumnChars = 512

// storable makes row one Postgres accepts: a batch is one INSERT, so a single
// refused row loses every row flushed with it. \ufffd has \u0000's shape, so the
// swap keeps the JSON valid even inside an escaped backslash.
func storable(row models.RequestLog) models.RequestLog {
	row.Path = text(row.Path)
	if path := []rune(row.Path); len(path) > pathColumnChars {
		row.Path = string(path[:pathColumnChars])
	}
	row.Query = strings.ReplaceAll(row.Query, `\u0000`, `\ufffd`)
	for _, body := range []*string{row.RequestBody, row.ResponseBody} {
		if body != nil {
			*body = text(*body)
		}
	}
	return row
}

func text(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\x00", "\uFFFD")
}

func (w *Writer) write(rows []models.RequestLog) {
	if err := w.sink.CreateBatch(context.Background(), rows); err != nil {
		facades.Log().Errorf("request log writer: batch insert: %v", err)
	}
}

func (w *Writer) Signature() string { return "request_log_writer" }

func (w *Writer) ShouldRun() bool { return true }

// Run has nothing to start. NewWriter already started the flush loop, so a
// writer resolved where Start never runs — a test, an artisan command — still
// flushes.
func (w *Writer) Run() error { return nil }

// Shutdown flushes the rows still buffered, once, and stops the loop. A row
// enqueued after it is written by Enqueue itself.
func (w *Writer) Shutdown() error {
	w.once.Do(func() {
		w.mu.Lock()
		w.closed = true
		w.mu.Unlock()
		close(w.done)
		w.wg.Wait()
	})
	return nil
}

func (w *Writer) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(defaultFlushEvery)
	defer ticker.Stop()

	batch := make([]models.RequestLog, 0, defaultBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		w.write(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-w.done:
			for {
				select {
				case row := <-w.ch:
					batch = append(batch, row)
					if len(batch) >= defaultBatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case row := <-w.ch:
			batch = append(batch, row)
			if len(batch) >= defaultBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
