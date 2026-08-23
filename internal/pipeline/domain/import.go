package domain

type ManifestFile struct {
	RelativePath string `json:"relative_path"`
	SHA256       string `json:"sha256"`
	Kind         string `json:"kind"`
}

type ReleaseManifest struct {
	CandidateID string         `json:"candidate_id"`
	ReleaseMBID string         `json:"release_mbid"`
	Files       []ManifestFile `json:"files"`
}

type LidarrImportPlan struct {
	RequestBody    []byte `json:"request_body"`
	AlbumID        int    `json:"album_id"`
	AlbumReleaseID int    `json:"album_release_id"`
	TrackIDs       []int  `json:"track_ids"`
}

type FinalFile struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	TrackID     int    `json:"track_id,omitempty"`
	SidecarPath string `json:"sidecar_path,omitempty"`
}

type ImportVerification struct {
	Complete             bool              `json:"complete"`
	Files                []FinalFile       `json:"files"`
	ReconciliationHashes map[string]string `json:"reconciliation_hashes,omitempty"`
	Reason               string            `json:"reason,omitempty"`
}

type ImportIntent struct {
	ID                string           `json:"id"`
	IdempotencyKey    string           `json:"idempotency_key"`
	CandidateID       string           `json:"candidate_id"`
	TargetReleaseMBID string           `json:"target_release_mbid"`
	RequestHash       string           `json:"request_hash"`
	Manifest          ReleaseManifest  `json:"manifest"`
	Plan              LidarrImportPlan `json:"plan"`
	DownloadID        string           `json:"download_id,omitempty"`
}
