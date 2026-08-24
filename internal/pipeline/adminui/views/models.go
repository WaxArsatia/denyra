package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adminui/assets"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
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
	Shell             Shell
	Items             []application.SubmissionSummary
	UploadSessions    []domain.UploadSession
	UploadConcurrency int
	Next              string
	Error             string
}
type IncomingDetailPage struct {
	Shell   Shell
	Preview domain.SubmissionPreview
	Error   string
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
type UnmanagedPage struct {
	Shell                Shell
	Items                []application.UnmanagedSummary
	Query, Status, Error string
}
type MigrationPage struct {
	Shell  Shell
	Detail application.MigrationBatchDetail
	Error  string
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
	case "approved", "imported", "exact_match", "migrated":
		return "approved"
	case "superseded", "cancelled":
		return "settled"
	default:
		return ""
	}
}

func MigrationStateLabel(state string) string {
	switch state {
	case "NO_MATCH":
		return "No match"
	case "AMBIGUOUS":
		return "Ambiguous"
	case "EXACT_MATCH":
		return "Exact candidate"
	case "FAILED_RETRYABLE":
		return "Error"
	case "CHECK_PENDING", "CHECKING":
		return "Checking"
	case "MIGRATED":
		return "Migrated"
	case "CONFIRMED":
		return "Confirmed"
	case "LIDARR_CATALOG_READY":
		return "Catalog ready"
	case "IMPORT_SUBMITTED", "RECONCILING":
		return "Importing"
	default:
		return state
	}
}

func MigrationPolling(detail application.MigrationBatchDetail) bool {
	for _, item := range detail.Items {
		if item.State == "CHECK_PENDING" || item.State == "CHECKING" || item.State == "CONFIRMED" || item.State == "LIDARR_CATALOG_READY" || item.State == "IMPORT_SUBMITTED" || item.State == "RECONCILING" {
			return true
		}
	}
	return false
}

func Millis(value int64) string { return fmt.Sprintf("%d ms", value) }
func ExactReleaseMBID(preview domain.SubmissionPreview) string {
	if preview.Identity == nil {
		return ""
	}
	return preview.Identity.ExactReleaseMBID
}
func OptionalMillis(value *int64) string {
	if value == nil {
		return "missing"
	}
	return Millis(*value)
}
