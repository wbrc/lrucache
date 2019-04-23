package lrucache

import (
	"bytes"
	"container/list"
	"errors"
	"io"
	"runtime"
	"sync"
	"time"
)

var (
	ErrElementNotfound = errors.New("element not found")
)

type cacheElem struct {
	key  string
	blob []byte
	exp  int64
}

type cache struct {
	elements      map[string]*list.Element
	recency       *list.List
	m             *sync.Mutex
	size, maxSize int
	defaultExpire time.Duration
	done          chan struct{}
}

// Store data using key. Optionally, the element will expire after exp
func (c *cache) Store(key string, data []byte, exp ...time.Duration) {
	c.m.Lock()
	defer c.m.Unlock()
	for c.size+len(data) > c.maxSize {
		del := c.recency.Back()
		c.recency.Remove(del)
		delete(c.elements, del.Value.(cacheElem).key)
		c.size -= len(del.Value.(cacheElem).blob)
	}

	expTime := time.Now().Add(c.defaultExpire).Unix()
	if exp != nil && len(exp) > 0 {
		expTime = time.Now().Add(exp[0]).Unix()
	}

	elem := c.recency.PushFront(cacheElem{
		key:  key,
		blob: data,
		exp:  expTime,
	})
	c.elements[key] = elem
}

// Get returns an io.Reader for the data associated with key
func (c *cache) Get(key string) (io.Reader, error) {
	c.m.Lock()
	defer c.m.Unlock()
	if elem, ok := c.elements[key]; ok {
		if elem.Value.(cacheElem).exp > 0 && elem.Value.(cacheElem).exp < time.Now().Unix() {
			delete(c.elements, elem.Value.(cacheElem).key)
			c.recency.Remove(elem)
			c.size -= len(elem.Value.(cacheElem).blob)
			return nil, ErrElementNotfound
		}
		c.recency.MoveToFront(elem)
		return bytes.NewReader(elem.Value.(cacheElem).blob), nil
	}
	return nil, ErrElementNotfound
}

func mrproper(c *cache, interval time.Duration) {
	timer := time.NewTimer(interval)
	for {
		select {
		case <-c.done:
			return
		case <-timer.C:
			now := time.Now().Unix()
			c.m.Lock()
			for e := c.recency.Front(); e != nil; e = e.Next() {
				if e.Value.(cacheElem).exp < now {
					delete(c.elements, e.Value.(cacheElem).key)
					c.recency.Remove(e)
					c.size -= len(e.Value.(cacheElem).blob)
				}
			}
			c.m.Unlock()
			timer.Reset(interval)
		}
	}
}

type Cache struct {
	*cache
}

// New creates a new cache with a maximum size and an optional default expiration exp
func New(conf Configuration) *Cache {
	if conf.MaxSize <= 0 {
		conf.MaxSize = 64 * 1024 * 1024
	}

	c := &cache{
		elements:      make(map[string]*list.Element),
		recency:       list.New(),
		m:             &sync.Mutex{},
		maxSize:       conf.MaxSize,
		defaultExpire: conf.DefaultExpire,
		done:          make(chan struct{}),
	}

	C := &Cache{c}

	if conf.CleanInterval > 0 {
		go mrproper(c, conf.CleanInterval)
		runtime.SetFinalizer(C, func(cp *Cache) {
			close(cp.done)
		})
	}

	return C
}

type Configuration struct {
	MaxSize       int
	DefaultExpire time.Duration
	CleanInterval time.Duration
}
