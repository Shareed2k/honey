// Package queue provides asynchronous task execution interfaces and implementations.
package queue

import "errors"

// ErrQueueFull is returned when the queue cannot accept more tasks.
var ErrQueueFull = errors.New("queue is full")

// Queue defines the interface for asynchronous task execution.
type Queue interface {
	Submit(task func()) error
	Close()
}
