package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type Snapshot struct {
	CanonicalJSON []byte
	Hash          [sha256.Size]byte
}

type secretSnapshot struct {
	Source      string `json:"source"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type snapshotDocument struct {
	Config  Config                    `json:"config"`
	Secrets map[string]secretSnapshot `json:"secret_references"`
}

func NewSnapshot(cfg Config, auditKey []byte) (Snapshot, error) {
	if err := cfg.Validate(); err != nil {
		return Snapshot{}, err
	}
	redacted := cfg
	redacted.Secrets = SecretsConfig{}
	document := snapshotDocument{
		Config: redacted,
		Secrets: map[string]secretSnapshot{
			"audit_key":       snapshotSecret(cfg.Secrets.AuditKey, auditKey),
			"internal_bearer": snapshotSecret(cfg.Secrets.InternalBearer, auditKey),
			"lidarr_api_key":  snapshotSecret(cfg.Secrets.LidarrAPIKey, auditKey),
		},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode config snapshot: %w", err)
	}
	return Snapshot{CanonicalJSON: encoded, Hash: sha256.Sum256(encoded)}, nil
}

func snapshotSecret(secret SecretRef, auditKey []byte) secretSnapshot {
	result := secretSnapshot{Source: secret.Source, Name: secret.Name}
	if secret.Value == "" || len(auditKey) == 0 {
		return result
	}
	mac := hmac.New(sha256.New, auditKey)
	_, _ = mac.Write([]byte(secret.Value))
	result.Fingerprint = hex.EncodeToString(mac.Sum(nil))
	return result
}
