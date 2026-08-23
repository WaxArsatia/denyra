package domain

import (
	"fmt"
	"strings"
	"time"
)

type CorrelationSource string

const (
	CorrelationQueue   CorrelationSource = "queue"
	CorrelationHistory CorrelationSource = "history"
)

type CorrelationRequest struct {
	AlbumID                          int64
	ReleaseGroupMBID, ReleaseMBID    string
	CommandID                        string
	QueueWatermark, HistoryWatermark int64
	StartedAt, Deadline              time.Time
}

type CorrelationObservation struct {
	Source                           CorrelationSource
	RecordID                         int64
	AlbumID                          int64
	ReleaseGroupMBID, ReleaseMBID    string
	CommandID, DownloadID, EventType string
	ObservedAt                       time.Time
	Raw                              []byte
}

type CorrelationMatch struct {
	Correlated bool
	Reason     string
}

func (request CorrelationRequest) Validate() error {
	if request.AlbumID <= 0 || request.ReleaseGroupMBID == "" || request.CommandID == "" {
		return fmt.Errorf("correlation request identity is incomplete")
	}
	if request.StartedAt.IsZero() || !request.Deadline.After(request.StartedAt) {
		return fmt.Errorf("correlation window is invalid")
	}
	return nil
}

func (request CorrelationRequest) Match(observation CorrelationObservation) CorrelationMatch {
	if err := request.Validate(); err != nil {
		return CorrelationMatch{Reason: err.Error()}
	}
	if observation.ObservedAt.Before(request.StartedAt) || observation.ObservedAt.After(request.Deadline) {
		return CorrelationMatch{Reason: "observation is outside the correlation window"}
	}
	watermark := request.HistoryWatermark
	if observation.Source == CorrelationQueue {
		watermark = request.QueueWatermark
	} else if observation.Source != CorrelationHistory {
		return CorrelationMatch{Reason: "unknown observation source"}
	}
	if observation.RecordID <= watermark {
		return CorrelationMatch{Reason: "record does not advance its source watermark"}
	}
	if observation.AlbumID != request.AlbumID || observation.ReleaseGroupMBID != request.ReleaseGroupMBID {
		return CorrelationMatch{Reason: "album or release-group identity mismatch"}
	}
	if request.ReleaseMBID != "" && observation.ReleaseMBID != request.ReleaseMBID {
		return CorrelationMatch{Reason: "selected release identity mismatch"}
	}
	if request.CommandID != "UNKNOWN" && observation.CommandID != "" && observation.CommandID != request.CommandID {
		return CorrelationMatch{Reason: "command context mismatch"}
	}
	switch observation.Source {
	case CorrelationQueue:
		if strings.TrimSpace(observation.DownloadID) == "" {
			return CorrelationMatch{Reason: "queue observation has no download identity"}
		}
	case CorrelationHistory:
		if !strings.EqualFold(observation.EventType, "grabbed") {
			return CorrelationMatch{Reason: "history observation is not a grab"}
		}
	}
	return CorrelationMatch{Correlated: true, Reason: "correlated primary grab"}
}
