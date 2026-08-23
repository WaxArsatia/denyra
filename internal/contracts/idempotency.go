package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

var ErrIdempotencyConflict = errors.New("idempotency key conflicts with prior request")

type IdempotencyRecord struct {
	Key            string    `json:"key"`
	Operation      string    `json:"operation"`
	RequestHash    string    `json:"request_hash"`
	ResponseStatus int       `json:"response_status"`
	ResponseHash   string    `json:"response_hash"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func (r IdempotencyRecord) ValidateReuse(operation, requestHash string) error {
	if r.Operation != operation || r.RequestHash != requestHash {
		return ErrIdempotencyConflict
	}
	return nil
}

func DecodeStrictJSON(reader io.Reader, limit int64, destination any) error {
	if limit <= 0 {
		return fmt.Errorf("JSON body limit must be positive")
	}
	limited := &io.LimitedReader{R: reader, N: limit + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read JSON body: %w", err)
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("JSON body exceeds %d bytes", limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON body contains multiple values")
		}
		return fmt.Errorf("decode JSON trailer: %w", err)
	}
	return nil
}
