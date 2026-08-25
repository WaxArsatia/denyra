package handlers

import (
	"sync"
	"time"
)

type LoginThrottle struct {
	mutex                           sync.Mutex
	entries                         map[[32]byte]loginThrottleEntry
	failures, capacity              int
	window, baseDelay, maximumDelay time.Duration
}

type loginThrottleEntry struct {
	failures               int
	firstFailure, lastSeen time.Time
	blockedUntil           time.Time
	audited                bool
}

func NewLoginThrottle(failures int, window, baseDelay, maximumDelay time.Duration, capacity int) *LoginThrottle {
	return &LoginThrottle{entries: make(map[[32]byte]loginThrottleEntry), failures: failures, window: window, baseDelay: baseDelay, maximumDelay: maximumDelay, capacity: capacity}
}

func (throttle *LoginThrottle) Allow(key [32]byte, now time.Time) (time.Duration, bool) {
	if throttle == nil {
		return 0, true
	}
	throttle.mutex.Lock()
	defer throttle.mutex.Unlock()
	throttle.expire(now)
	entry, found := throttle.entries[key]
	if !found {
		return 0, true
	}
	entry.lastSeen = now
	throttle.entries[key] = entry
	if now.Before(entry.blockedUntil) {
		return entry.blockedUntil.Sub(now), false
	}
	return 0, true
}

func (throttle *LoginThrottle) Failure(key [32]byte, now time.Time) (blocked bool) {
	if throttle == nil {
		return false
	}
	throttle.mutex.Lock()
	defer throttle.mutex.Unlock()
	throttle.expire(now)
	entry, found := throttle.entries[key]
	if !found {
		throttle.makeRoom(now)
		entry.firstFailure = now
	}
	entry.failures++
	entry.lastSeen = now
	if entry.failures >= throttle.failures {
		delay := throttle.delay(entry.failures - throttle.failures)
		entry.blockedUntil = now.Add(delay)
		blocked = !entry.audited
		entry.audited = true
	}
	throttle.entries[key] = entry
	return blocked
}

func (throttle *LoginThrottle) Success(key [32]byte) {
	if throttle == nil {
		return
	}
	throttle.mutex.Lock()
	delete(throttle.entries, key)
	throttle.mutex.Unlock()
}

func (throttle *LoginThrottle) delay(exponent int) time.Duration {
	delay := throttle.baseDelay
	for exponent > 0 && delay < throttle.maximumDelay {
		if delay > throttle.maximumDelay/2 {
			delay = throttle.maximumDelay
			break
		}
		delay *= 2
		exponent--
	}
	if delay > throttle.maximumDelay {
		return throttle.maximumDelay
	}
	return delay
}

func (throttle *LoginThrottle) expire(now time.Time) {
	for key, entry := range throttle.entries {
		if !entry.firstFailure.IsZero() && !now.Before(entry.firstFailure.Add(throttle.window)) && !now.Before(entry.blockedUntil) {
			delete(throttle.entries, key)
		}
	}
}

func (throttle *LoginThrottle) makeRoom(now time.Time) {
	throttle.expire(now)
	if throttle.capacity <= 0 || len(throttle.entries) < throttle.capacity {
		return
	}
	var oldestKey [32]byte
	var oldest time.Time
	for key, entry := range throttle.entries {
		if oldest.IsZero() || entry.lastSeen.Before(oldest) {
			oldestKey, oldest = key, entry.lastSeen
		}
	}
	delete(throttle.entries, oldestKey)
}

func (throttle *LoginThrottle) size() int {
	throttle.mutex.Lock()
	defer throttle.mutex.Unlock()
	return len(throttle.entries)
}
