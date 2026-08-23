package domain

import (
	"testing"
	"time"
)

func TestCorrelationRequiresIdentityWatermarkAndWindow(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	request := CorrelationRequest{
		AlbumID:          42,
		ReleaseGroupMBID: "12345678-1234-1234-1234-123456789abc",
		ReleaseMBID:      "abcdefab-1234-5678-9abc-abcdefabcdef",
		CommandID:        "77",
		QueueWatermark:   10,
		HistoryWatermark: 20,
		StartedAt:        started,
		Deadline:         started.Add(time.Minute),
	}
	valid := CorrelationObservation{
		Source:           CorrelationHistory,
		RecordID:         21,
		AlbumID:          42,
		ReleaseGroupMBID: request.ReleaseGroupMBID,
		ReleaseMBID:      request.ReleaseMBID,
		CommandID:        "77",
		EventType:        "grabbed",
		ObservedAt:       started.Add(30 * time.Second),
	}
	if match := request.Match(valid); !match.Correlated {
		t.Fatalf("valid observation rejected: %+v", match)
	}

	tests := map[string]func(*CorrelationObservation){
		"watermark":     func(value *CorrelationObservation) { value.RecordID = 20 },
		"wrong album":   func(value *CorrelationObservation) { value.AlbumID = 99 },
		"wrong group":   func(value *CorrelationObservation) { value.ReleaseGroupMBID = "11111111-2222-3333-4444-555555555555" },
		"wrong release": func(value *CorrelationObservation) { value.ReleaseMBID = "11111111-2222-3333-4444-555555555555" },
		"wrong command": func(value *CorrelationObservation) { value.CommandID = "88" },
		"late":          func(value *CorrelationObservation) { value.ObservedAt = request.Deadline.Add(time.Nanosecond) },
		"not a grab":    func(value *CorrelationObservation) { value.EventType = "downloadFolderImported" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if match := request.Match(candidate); match.Correlated {
				t.Fatalf("invalid observation correlated: %+v", candidate)
			}
		})
	}
}

func TestCorrelationAllowsHistoryGrabWithoutDownloadID(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	request := CorrelationRequest{AlbumID: 42, ReleaseGroupMBID: "12345678-1234-1234-1234-123456789abc", CommandID: "77", HistoryWatermark: 20, StartedAt: started, Deadline: started.Add(time.Minute)}
	observation := CorrelationObservation{Source: CorrelationHistory, RecordID: 21, AlbumID: 42, ReleaseGroupMBID: request.ReleaseGroupMBID, EventType: "grabbed", ObservedAt: started.Add(time.Second)}
	if match := request.Match(observation); !match.Correlated {
		t.Fatalf("history grab without download ID rejected: %+v", match)
	}
}

func TestCorrelationRejectsQueueWithoutDownloadIdentity(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	request := CorrelationRequest{AlbumID: 42, ReleaseGroupMBID: "12345678-1234-1234-1234-123456789abc", QueueWatermark: 10, StartedAt: started, Deadline: started.Add(time.Minute)}
	observation := CorrelationObservation{Source: CorrelationQueue, RecordID: 11, AlbumID: 42, ReleaseGroupMBID: request.ReleaseGroupMBID, ObservedAt: started.Add(time.Second)}
	if match := request.Match(observation); match.Correlated {
		t.Fatalf("unidentified queue record correlated: %+v", match)
	}
}
