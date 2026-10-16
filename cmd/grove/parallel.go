package main

import "runtime"

// maxStatusWorkers caps concurrent `git status` runs. Each one already uses
// several threads to read the index, so more workers than this mostly contend
// for disk and memory bandwidth on a large repo.
const maxStatusWorkers = 8

// statusWorkers picks how many status checks run at once.
func statusWorkers() int {
	w := runtime.NumCPU()
	if w > maxStatusWorkers {
		w = maxStatusWorkers
	}
	return w
}

// runParallel calls fn(i) for every i in [0,n) on at most workers goroutines,
// starting them in index order. It returns one channel per index that is
// closed once fn(i) has returned, so callers can consume results in order as
// they land instead of waiting for the slowest one.
func runParallel(n, workers int, fn func(i int)) []chan struct{} {
	// One completion channel per index
	done := make([]chan struct{}, n)
	for i := range done {
		done[i] = make(chan struct{})
	}

	// Queue every index up front so workers drain them in order
	next := make(chan int, n)
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)

	// Start the workers, never more than there are items
	if workers > n {
		workers = n
	}
	if workers < 1 && n > 0 {
		workers = 1
	}
	for w := 0; w < workers; w++ {
		go func() {
			for i := range next {
				fn(i)
				close(done[i])
			}
		}()
	}
	return done
}
