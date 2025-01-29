//go:build windows
// +build windows

package k6provider

import (
	"time"
)

// Fake implementation for windows
type Pruner struct{}

// NewPruner creates a [] given its high-water-mark limit, and the
// prune interval
func NewPruner(dir string, hwm int64, pruneInterval time.Duration) *Pruner {
	return &Pruner{}
}

// Touch update access time because reading the file not always updates it
func (p *Pruner) Touch(binPath string) {
}

// Prune the cache of least recently used files
func (p *Pruner) Prune() error {
	return nil
}
