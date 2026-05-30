package stream

import "sync"

// Pool is a generic sync.Pool wrapper for StreamConverter states.
type Pool struct {
	pool sync.Pool
}

// NewPool creates a pool with the given factory function.
func NewPool(factory func() StreamConverter) *Pool {
	return &Pool{
		pool: sync.Pool{New: func() interface{} { return factory() }},
	}
}

// Get retrieves a StreamConverter from the pool.
func (p *Pool) Get() StreamConverter {
	return p.pool.Get().(StreamConverter)
}

// Put returns a StreamConverter to the pool.
func (p *Pool) Put(c StreamConverter) {
	c.Reset()
	p.pool.Put(c)
}
