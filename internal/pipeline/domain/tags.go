package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var ManagedMusicBrainzFields = []string{
	"MUSICBRAINZ_TRACKID",
	"MUSICBRAINZ_RELEASETRACKID",
	"MUSICBRAINZ_ALBUMID",
	"MUSICBRAINZ_RELEASEGROUPID",
	"MUSICBRAINZ_ARTISTID",
	"MUSICBRAINZ_ALBUMARTISTID",
}

var ManagedTagFields = []string{
	"TITLE", "ARTIST", "ALBUM", "ALBUMARTIST", "TRACKNUMBER", "DISCNUMBER", "DATE", "GENRE", "ISRC",
	"MUSICBRAINZ_TRACKID", "MUSICBRAINZ_RELEASETRACKID", "MUSICBRAINZ_ALBUMID", "MUSICBRAINZ_RELEASEGROUPID",
	"MUSICBRAINZ_ARTISTID", "MUSICBRAINZ_ALBUMARTISTID",
}

type TagSet map[string][]string

type TagInput struct {
	Title            string
	Artists          []ArtistCredit
	Album            string
	AlbumArtists     []ArtistCredit
	TrackNumber      int
	DiscNumber       int
	Date             string
	Genres           []string
	ISRCs            []string
	RecordingMBID    string
	ReleaseTrackMBID string
	ReleaseMBID      string
	ReleaseGroupMBID string
}

var datePattern = regexp.MustCompile(`^[0-9]{4}(-[0-9]{2}(-[0-9]{2})?)?$`)
var isrcPattern = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{3}[0-9]{7}$`)

func CanonicalTags(input TagInput) (TagSet, error) {
	if input.TrackNumber <= 0 || input.DiscNumber <= 0 {
		return nil, fmt.Errorf("track and disc numbers must be positive")
	}
	if !datePattern.MatchString(input.Date) {
		return nil, fmt.Errorf("date must preserve MusicBrainz YYYY, YYYY-MM, or YYYY-MM-DD precision")
	}
	result := TagSet{}
	for field, values := range map[string][]string{
		"TITLE": {input.Title}, "ALBUM": {input.Album}, "TRACKNUMBER": {strconv.Itoa(input.TrackNumber)},
		"DISCNUMBER": {strconv.Itoa(input.DiscNumber)}, "DATE": {input.Date},
	} {
		normalized, err := NormalizeValues(values, false)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		result[field] = normalized
	}
	artistNames, artistIDs, err := alignedCredits(input.Artists)
	if err != nil {
		return nil, fmt.Errorf("artists: %w", err)
	}
	albumArtistNames, albumArtistIDs, err := alignedCredits(input.AlbumArtists)
	if err != nil {
		return nil, fmt.Errorf("album artists: %w", err)
	}
	result["ARTIST"], result["MUSICBRAINZ_ARTISTID"] = artistNames, artistIDs
	result["ALBUMARTIST"], result["MUSICBRAINZ_ALBUMARTISTID"] = albumArtistNames, albumArtistIDs
	for field, mbid := range map[string]string{
		"MUSICBRAINZ_TRACKID": input.RecordingMBID, "MUSICBRAINZ_RELEASETRACKID": input.ReleaseTrackMBID,
		"MUSICBRAINZ_ALBUMID": input.ReleaseMBID, "MUSICBRAINZ_RELEASEGROUPID": input.ReleaseGroupMBID,
	} {
		canonical, err := CanonicalMBID(mbid)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		result[field] = []string{canonical}
	}
	genres, err := NormalizeValues(input.Genres, true)
	if err != nil {
		return nil, fmt.Errorf("GENRE: %w", err)
	}
	result["GENRE"] = genres
	isrcs := make([]string, 0, len(input.ISRCs))
	for _, value := range input.ISRCs {
		value = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
		if !isrcPattern.MatchString(value) {
			return nil, fmt.Errorf("invalid ISRC %q", value)
		}
		isrcs = append(isrcs, value)
	}
	isrcs, err = NormalizeValues(isrcs, true)
	if err != nil {
		return nil, err
	}
	result["ISRC"] = isrcs
	return result, nil
}

func alignedCredits(credits []ArtistCredit) ([]string, []string, error) {
	if len(credits) == 0 {
		return nil, nil, fmt.Errorf("at least one credit is required")
	}
	names, ids := make([]string, 0, len(credits)), make([]string, 0, len(credits))
	for _, credit := range credits {
		name, err := NormalizeTagValue(credit.Name)
		if err != nil {
			return nil, nil, err
		}
		id, err := CanonicalMBID(credit.ArtistMBID)
		if err != nil {
			return nil, nil, err
		}
		names, ids = append(names, name), append(ids, id)
	}
	return names, ids, nil
}

func (t TagSet) OrderedEntries() ([]string, error) {
	entries := make([]string, 0)
	for _, field := range ManagedTagFields {
		values, ok := t[field]
		if !ok {
			return nil, fmt.Errorf("managed field %s is missing", field)
		}
		for _, value := range values {
			normalized, err := NormalizeTagValue(value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", field, err)
			}
			entries = append(entries, field+"="+normalized)
		}
	}
	return entries, nil
}

func IsManagedTag(field string) bool {
	field = strings.ToUpper(field)
	return slices.Contains(ManagedTagFields, field) || field == "MUSICBRAINZ_RECORDINGID"
}
