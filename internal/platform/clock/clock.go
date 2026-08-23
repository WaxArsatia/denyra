// Package clock provides injectable UTC time sources.
package clock

import "time"

type Clock interface {
	Now() time.Time
}

type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

type Fake struct {
	Time time.Time
}

func (f Fake) Now() time.Time { return f.Time.UTC() }
