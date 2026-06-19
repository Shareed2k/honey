package queue

import "github.com/panjf2000/ants/v2"

// AntsQueue is an in-memory queue implementation using ants pool.
type AntsQueue struct {
	pool *ants.Pool
}

// NewAntsQueue creates a new AntsQueue with the given pool size.
func NewAntsQueue(size int) (*AntsQueue, error) {
	pool, err := ants.NewPool(size, ants.WithPreAlloc(true), ants.WithNonblocking(true))
	if err != nil {
		return nil, err
	}
	return &AntsQueue{pool: pool}, nil
}

// Submit adds a task to the queue. Returns ErrQueueFull if the queue is full.
func (q *AntsQueue) Submit(task func()) error {
	err := q.pool.Submit(task)
	if err == ants.ErrPoolOverload {
		return ErrQueueFull
	}
	return err
}

// Close releases the pool resources.
func (q *AntsQueue) Close() {
	q.pool.Release()
}
