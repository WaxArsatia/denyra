package persistence

import (
	"database/sql"
	"time"
)

type Repositories struct {
	DB  *sql.DB
	Now func() time.Time
}

func New(db *sql.DB, now func() time.Time) *Repositories {
	if now == nil {
		now = time.Now
	}
	return &Repositories{DB: db, Now: now}
}
