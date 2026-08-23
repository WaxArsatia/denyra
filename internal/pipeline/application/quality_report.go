package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type QualityIntentStore interface {
	PutQualityIntent(context.Context, string, string, string, []byte, time.Time) error
	CompleteQualityIntent(context.Context, string, int, string, time.Time) error
}

type QualityCallback interface {
	ReportApproved(context.Context, []byte, string, string) (contracts.CallbackResult, error)
}

type QualityReporter struct {
	Store    QualityIntentStore
	Callback QualityCallback
	Now      func() time.Time
}

func (r QualityReporter) Report(ctx context.Context, approved contracts.CandidateApproved, idempotencyKey string) error {
	if r.Store == nil || r.Callback == nil || idempotencyKey == "" {
		return fmt.Errorf("quality reporter is not configured")
	}
	payload, err := json.Marshal(approved)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(payload)
	requestHash := hex.EncodeToString(hash[:])
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	if err := r.Store.PutQualityIntent(ctx, idempotencyKey, approved.CandidateID, requestHash, payload, now().UTC()); err != nil {
		return err
	}
	response, err := r.Callback.ReportApproved(ctx, payload, approved.RequestID, idempotencyKey)
	if err != nil {
		return err
	}
	return r.Store.CompleteQualityIntent(ctx, idempotencyKey, response.StatusCode, response.ResponseSHA256, now().UTC())
}

func ContractQuality(value domain.QualityVector) contracts.QualityVector {
	return contracts.QualityVector{
		IdentityRank: value.IdentityRank, EditionRank: value.EditionRank, QualityWarningCount: value.QualityWarningCount,
		SourceConfidence: value.SourceConfidence, BitDepth: value.BitDepth, SampleRate: value.SampleRate,
	}
}
