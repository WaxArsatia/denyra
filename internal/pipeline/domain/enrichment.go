package domain

type LyricsQuery struct {
	TrackName  string `json:"track_name"`
	ArtistName string `json:"artist_name"`
	AlbumName  string `json:"album_name"`
	DurationMS int64  `json:"duration_ms"`
}

type LyricsResult struct {
	ID           int64  `json:"id"`
	Instrumental bool   `json:"instrumental"`
	Plain        string `json:"plain_lyrics"`
	Synced       string `json:"synced_lyrics"`
	WordSynced   string `json:"word_synced_lyrics,omitempty"`
}

type ProviderEvidence struct {
	Provider       string `json:"provider"`
	Endpoint       string `json:"endpoint"`
	StatusCode     int    `json:"status_code"`
	ResponseSHA256 string `json:"response_sha256"`
	ResponseBody   []byte `json:"response_body"`
}
