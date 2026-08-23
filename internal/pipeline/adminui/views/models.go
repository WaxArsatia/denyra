package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adminui/assets"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

type Shell struct {
	Deployment     string
	ConfigSnapshot string
	Readiness      string
	ReadinessClass string
	Username       string
	CSRFToken      string
	Assets         assets.Paths
}

type ReviewsPage struct {
	Shell Shell
	Items []application.ReviewSummary
	Next  string
	Error string
}
type ReviewPage struct {
	Shell   Shell
	Detail  application.ReviewDetail
	Error   string
	Confirm string
}
type IncomingPage struct {
	Shell Shell
	Items []application.SubmissionSummary
	Next  string
	Error string
}
type AuditPage struct {
	Shell Shell
	Items []application.AuditSummary
	Next  string
	Error string
}
type SessionsPage struct {
	Shell Shell
	Items []application.SessionSummary
	Error string
}
type AcquisitionPage struct {
	Shell    Shell
	Evidence application.AcquisitionEvidence
	Error    string
	Degraded bool
}
type AccountPage struct {
	Shell Shell
	Error string
}

func FormatTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.UTC().Format("2006-01-02 15:04:05Z")
}

func StateClass(value any) string {
	state := strings.ToLower(fmt.Sprint(value))
	switch state {
	case "review_required", "waiting_resubmit":
		return "review"
	case "quarantined", "rejected":
		return "blocked"
	case "approved", "imported":
		return "approved"
	case "superseded", "cancelled":
		return "settled"
	default:
		return ""
	}
}

func Millis(value int64) string { return fmt.Sprintf("%d ms", value) }
func OptionalMillis(value *int64) string {
	if value == nil {
		return "missing"
	}
	return Millis(*value)
}
