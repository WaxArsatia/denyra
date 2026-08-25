package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/platform/logsafe"
)

type Client struct {
	BaseURL, Bearer string
	HTTP            *http.Client
	ResponseLimit   int64
}

func (client Client) ListAcquisitions(ctx context.Context, limit int, cursor, state string) (application.AcquisitionPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if state != "" {
		query.Set("state", state)
	}
	var response contracts.AcquisitionJobPage
	if err := client.get(ctx, "/internal/acquisitions?"+query.Encode(), &response); err != nil {
		return application.AcquisitionPage{}, err
	}
	if len(response.Items) > 100 {
		return application.AcquisitionPage{}, fmt.Errorf("gateway acquisition page exceeds item limit")
	}
	page := application.AcquisitionPage{Items: make([]application.AcquisitionSummary, len(response.Items)), Next: response.Next}
	for index, item := range response.Items {
		page.Items[index] = application.AcquisitionSummary{JobID: item.JobID, State: item.State, ReleaseGroupID: item.ReleaseGroupMBID, SelectedReleaseID: item.SelectedReleaseMBID, Revision: item.StateRevision, AlbumID: item.LidarrAlbumID, PrimaryAttempt: item.PrimaryAttempt, FallbackAttempt: item.FallbackAttempt, NextRetryAt: item.NextRetryAt, UpdatedAt: item.UpdatedAt}
	}
	return page, nil
}

func (client Client) AcquisitionEvidence(ctx context.Context, jobID string) (application.AcquisitionEvidence, error) {
	if jobID == "" {
		return application.AcquisitionEvidence{}, fmt.Errorf("gateway acquisition job is required")
	}
	var detail contracts.AcquisitionJobDetail
	if err := client.get(ctx, "/internal/acquisitions/"+url.PathEscape(jobID), &detail); err != nil {
		return application.AcquisitionEvidence{}, err
	}
	if len(detail.Transitions) > 100 || len(detail.Attempts) > 100 || len(detail.Candidates) > 100 || len(detail.Correlation) > 100 {
		return application.AcquisitionEvidence{}, fmt.Errorf("gateway acquisition detail exceeds section limit")
	}
	result := application.AcquisitionEvidence{
		JobID: detail.Job.JobID, State: detail.Job.State, ReleaseGroupID: detail.Job.ReleaseGroupMBID, SelectedReleaseID: detail.Job.SelectedReleaseMBID,
		Revision: detail.Job.StateRevision, AlbumID: detail.Job.LidarrAlbumID, PrimaryAttempt: detail.Job.PrimaryAttempt, FallbackAttempt: detail.Job.FallbackAttempt,
		NextRetryAt: detail.Job.NextRetryAt, ObservedAt: detail.Job.UpdatedAt, TruncatedSections: append([]string(nil), detail.TruncatedSections...),
		Transitions: make([]application.AcquisitionTransition, len(detail.Transitions)), Attempts: make([]application.AcquisitionAttempt, len(detail.Attempts)),
		Candidates: make([]application.AcquisitionCandidate, len(detail.Candidates)), Correlations: make([]application.AcquisitionCorrelation, len(detail.Correlation)),
	}
	for index, item := range detail.Transitions {
		result.Transitions[index] = application.AcquisitionTransition{Actor: item.Actor, Reason: item.Reason, PreviousState: item.PreviousState, NewState: item.NewState, Revision: item.Revision, OccurredAt: item.OccurredAt}
	}
	for index, item := range detail.Attempts {
		result.Attempts[index] = application.AcquisitionAttempt{ID: item.ID, Kind: item.Kind, Provider: item.Provider, Outcome: item.Outcome, ErrorClass: item.ErrorClass, Message: capMessage(logsafe.RedactText(item.Message), 2<<10), Number: item.Number, StartedAt: item.StartedAt, CompletedAt: item.CompletedAt}
	}
	for index, item := range detail.Candidates {
		result.Candidates[index] = application.AcquisitionCandidate{CandidateID: item.CandidateID, Source: item.Source, SourceLocator: item.SourceLocator, DownloadID: item.DownloadID, OutputSHA256: item.OutputSHA256, CompletedAt: item.CompletedAt, CreatedAt: item.CreatedAt}
	}
	for index, item := range detail.Correlation {
		result.Correlations[index] = application.AcquisitionCorrelation{SourceKind: item.SourceKind, SourceRecordID: item.SourceRecordID, CommandID: item.CommandID, DownloadID: item.DownloadID, EvidenceSHA256: item.EvidenceSHA256, ObservedAt: item.ObservedAt}
	}
	return result, nil
}

func (client Client) get(ctx context.Context, path string, target any) error {
	if client.BaseURL == "" || client.Bearer == "" || client.HTTP == nil || client.ResponseLimit <= 0 {
		return fmt.Errorf("gateway acquisition client is not configured")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(client.BaseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+client.Bearer)
	request.Header.Set("Accept", "application/json")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, client.ResponseLimit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > client.ResponseLimit {
		return fmt.Errorf("gateway acquisition response exceeds limit")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("gateway acquisition HTTP status %d", response.StatusCode)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	return nil
}

func capMessage(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
