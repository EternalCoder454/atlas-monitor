package stats

import "sync"

// HistLen is the number of samples kept in every graph ring buffer (60s @ 1Hz).
const HistLen = 60

// RingBuffer is a fixed-size circular buffer of float64 samples. It is
// pre-allocated and never grows after construction. It is safe for one writer
// (a collector goroutine) and many readers (the GTK main thread) concurrently.
type RingBuffer struct {
	mu   sync.RWMutex
	data [HistLen]float64
	head int // index of the next write
	n    int // number of valid samples, capped at HistLen
}

// NewRingBuffer returns an empty, pre-allocated ring buffer.
func NewRingBuffer() *RingBuffer { return &RingBuffer{} }

// Push appends a sample, overwriting the oldest once full.
func (r *RingBuffer) Push(v float64) {
	r.mu.Lock()
	r.data[r.head] = v
	r.head = (r.head + 1) % HistLen
	if r.n < HistLen {
		r.n++
	}
	r.mu.Unlock()
}

// ReadInto copies the valid samples in chronological order (oldest first,
// newest last) into dst and returns the count written. dst must have len
// >= HistLen. No allocation occurs.
func (r *RingBuffer) ReadInto(dst []float64) int {
	r.mu.RLock()
	n := r.n
	for i := 0; i < n; i++ {
		dst[i] = r.data[(r.head-n+i+HistLen)%HistLen]
	}
	r.mu.RUnlock()
	return n
}

// Last returns the most recent sample, or 0 if empty.
func (r *RingBuffer) Last() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.n == 0 {
		return 0
	}
	return r.data[(r.head-1+HistLen)%HistLen]
}

// Max returns the largest valid sample, or 0 if empty. Used to auto-scale
// byte-rate graphs.
func (r *RingBuffer) Max() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	max := 0.0
	for i := 0; i < r.n; i++ {
		if r.data[i] > max {
			max = r.data[i]
		}
	}
	return max
}
