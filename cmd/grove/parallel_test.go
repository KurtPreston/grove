package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunParallelRunsEachIndexOnceWithinWorkerLimit(t *testing.T) {
	const n, workers = 20, 3
	var mu sync.Mutex
	seen := make(map[int]int)
	var running, peak int32

	// Record each call and the peak number running at once
	done := runParallel(n, workers, func(i int) {
		cur := atomic.AddInt32(&running, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		mu.Lock()
		seen[i]++
		mu.Unlock()
		atomic.AddInt32(&running, -1)
	})

	// Every index finishes exactly once without exceeding the limit
	for i := range done {
		<-done[i]
	}
	for i := 0; i < n; i++ {
		if seen[i] != 1 {
			t.Errorf("index %d ran %d times, want 1", i, seen[i])
		}
	}
	if peak > workers {
		t.Errorf("peak concurrency %d exceeds %d workers", peak, workers)
	}
}

func TestRunParallelHandlesEmptyAndZeroWorkers(t *testing.T) {
	if done := runParallel(0, 4, func(int) { t.Fatal("fn called for n=0") }); len(done) != 0 {
		t.Fatalf("n=0 should return no channels, got %d", len(done))
	}
	ran := false
	done := runParallel(1, 0, func(int) { ran = true })
	<-done[0]
	if !ran {
		t.Fatal("workers=0 should still run the item")
	}
}
