package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const (
	standardTrackFormat  = "{Album Title} ({Release Year})/{Artist Name} - {Album Title} - {track:00} - {Track Title}"
	multiDiscTrackFormat = "{Album Title} ({Release Year})/{Medium Format} {medium:00}/{Artist Name} - {Album Title} - {track:00} - {Track Title}"
	lidarrErrorBodyLimit = 16 << 10
)

type Lidarr struct {
	BaseURL     string
	APIKey      string
	SlskdURL    string
	SlskdAPIKey string
	HTTP        *http.Client
}

func (l Lidarr) Apply(ctx context.Context) (Outcome, error) {
	steps := []func(context.Context) (bool, error){
		l.ensureRootFolder,
		func(ctx context.Context) (bool, error) {
			return l.reconcileSingleton(ctx, "/api/v1/config/downloadclient", func(resource map[string]any) bool {
				return setMapValue(resource, "enableCompletedDownloadHandling", false)
			})
		},
		func(ctx context.Context) (bool, error) {
			return l.reconcileSingleton(ctx, "/api/v1/config/mediamanagement", func(resource map[string]any) bool {
				changed := setMapValue(resource, "importExtraFiles", true)
				return setMapValue(resource, "extraFileExtensions", "lrc,elrc,ttml") || changed
			})
		},
		func(ctx context.Context) (bool, error) {
			return l.reconcileSingleton(ctx, "/api/v1/config/naming", func(resource map[string]any) bool {
				changed := setMapValue(resource, "renameTracks", true)
				changed = setMapValue(resource, "standardTrackFormat", standardTrackFormat) || changed
				changed = setMapValue(resource, "multiDiscTrackFormat", multiDiscTrackFormat) || changed
				return setMapValue(resource, "artistFolderFormat", "{Artist Name}") || changed
			})
		},
		l.reconcileMetadata,
		l.reconcileSlskdDelayProfile,
		l.reconcileSlskdDownloadClient,
		l.reconcileSlskdIndexer,
	}
	changed := false
	for _, step := range steps {
		stepChanged, err := step(ctx)
		if err != nil {
			return Outcome{Service: "lidarr"}, err
		}
		changed = changed || stepChanged
	}
	message := "already configured"
	if changed {
		message = "configuration updated"
	}
	return Outcome{Service: "lidarr", Changed: changed, Message: message}, nil
}

func (l Lidarr) reconcileSlskdDelayProfile(ctx context.Context) (bool, error) {
	var profiles []map[string]any
	if err := l.get(ctx, "/api/v1/delayprofile", &profiles); err != nil {
		return false, err
	}
	for _, profile := range profiles {
		if !strings.EqualFold(textValue(profile["name"]), "Default") {
			continue
		}
		items, ok := profile["items"].([]any)
		if !ok {
			return false, fmt.Errorf("Lidarr default delay profile has no protocol items")
		}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok || textValue(item["protocol"]) != "SlskdDownloadProtocol" {
				continue
			}
			if !setMapValue(item, "allowed", true) {
				return false, nil
			}
			id, err := resourceID(profile)
			if err != nil {
				return false, err
			}
			return true, l.send(ctx, http.MethodPut, "/api/v1/delayprofile/"+id, profile)
		}
		return false, fmt.Errorf("Lidarr default delay profile lacks SlskdDownloadProtocol")
	}
	return false, fmt.Errorf("Lidarr default delay profile is unavailable")
}

func (l Lidarr) ensureRootFolder(ctx context.Context) (bool, error) {
	var roots []map[string]any
	if err := l.get(ctx, "/api/v1/rootfolder", &roots); err != nil {
		return false, err
	}
	for _, root := range roots {
		if root["path"] == "/data/library" {
			return false, nil
		}
	}
	qualityProfileID, err := l.profileID(ctx, "/api/v1/qualityprofile", "Lossless")
	if err != nil {
		return false, err
	}
	metadataProfileID, err := l.profileID(ctx, "/api/v1/metadataprofile", "Standard")
	if err != nil {
		return false, err
	}
	payload := map[string]any{
		"name": "Denyra Library", "path": "/data/library",
		"defaultQualityProfileId": qualityProfileID, "defaultMetadataProfileId": metadataProfileID,
	}
	return true, l.send(ctx, http.MethodPost, "/api/v1/rootfolder", payload)
}

func (l Lidarr) profileID(ctx context.Context, path, name string) (int, error) {
	var profiles []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := l.get(ctx, path, &profiles); err != nil {
		return 0, err
	}
	for _, profile := range profiles {
		if profile.ID > 0 && strings.EqualFold(profile.Name, name) {
			return profile.ID, nil
		}
	}
	return 0, fmt.Errorf("Lidarr profile %q is unavailable at %s", name, path)
}

func (l Lidarr) reconcileSingleton(ctx context.Context, path string, mutate func(map[string]any) bool) (bool, error) {
	var resource map[string]any
	if err := l.get(ctx, path, &resource); err != nil {
		return false, err
	}
	if !mutate(resource) {
		return false, nil
	}
	return true, l.send(ctx, http.MethodPut, path, resource)
}

func (l Lidarr) reconcileMetadata(ctx context.Context) (bool, error) {
	var resources []map[string]any
	if err := l.get(ctx, "/api/v1/metadata", &resources); err != nil {
		return false, err
	}
	for _, resource := range resources {
		if !strings.EqualFold(textValue(resource["implementation"]), "XbmcMetadata") {
			continue
		}
		changed := setMapValue(resource, "enable", true)
		fieldChanged, err := setNamedFields(resource, map[string]any{"artistImages": true, "albumImages": true})
		if err != nil {
			return false, fmt.Errorf("Lidarr Kodi/Emby metadata: %w", err)
		}
		changed = changed || fieldChanged
		if !changed {
			return false, nil
		}
		id, err := resourceID(resource)
		if err != nil {
			return false, fmt.Errorf("Lidarr Kodi/Emby metadata: %w", err)
		}
		return true, l.send(ctx, http.MethodPut, "/api/v1/metadata/"+id, resource)
	}
	return false, fmt.Errorf("Lidarr Kodi (XBMC) / Emby metadata consumer is unavailable")
}

func (l Lidarr) reconcileSlskdDownloadClient(ctx context.Context) (bool, error) {
	desired := map[string]any{
		"host": "slskd", "port": float64(5030), "useSsl": false, "urlBase": "",
		"apiKey": l.SlskdAPIKey, "repairConfiguration": false,
	}
	return l.reconcileSlskdResource(ctx, "/api/v1/downloadclient", "Denyra slskd", nil, desired)
}

func (l Lidarr) reconcileSlskdIndexer(ctx context.Context) (bool, error) {
	desiredProperties := map[string]any{
		"enableAutomaticSearch":   true,
		"enableInteractiveSearch": true,
	}
	desired := map[string]any{
		"baseUrl": strings.TrimRight(l.SlskdURL, "/") + "/", "apiKey": l.SlskdAPIKey,
		"minimumPeerUploadSpeed": float64(0), "maximumPeerQueueLength": float64(0),
		"allowIncompleteReleases": false, "verifyDurations": true,
	}
	return l.reconcileSlskdResource(ctx, "/api/v1/indexer", "Denyra slskd indexer", desiredProperties, desired)
}

func (l Lidarr) reconcileSlskdResource(ctx context.Context, path, name string, desiredProperties, desiredFields map[string]any) (bool, error) {
	var resources []map[string]any
	if err := l.get(ctx, path, &resources); err != nil {
		return false, err
	}
	for _, resource := range resources {
		if textValue(resource["implementation"]) != "Slskd" {
			continue
		}
		changed := setMapValue(resource, "name", name)
		changed = setMapValue(resource, "enable", true) || changed
		for property, value := range desiredProperties {
			changed = setMapValue(resource, property, value) || changed
		}
		fieldsChanged, err := setNamedFields(resource, desiredFields)
		if err != nil {
			return false, fmt.Errorf("Lidarr Slskd schema lacks field %s", missingField(err))
		}
		changed = changed || fieldsChanged
		if !changed {
			return false, nil
		}
		id, err := resourceID(resource)
		if err != nil {
			return false, err
		}
		return true, l.send(ctx, http.MethodPut, path+"/"+id, resource)
	}

	var schemas []map[string]any
	if err := l.get(ctx, path+"/schema", &schemas); err != nil {
		return false, err
	}
	for _, schema := range schemas {
		if textValue(schema["implementation"]) != "Slskd" {
			continue
		}
		setMapValue(schema, "name", name)
		setMapValue(schema, "enable", true)
		for property, value := range desiredProperties {
			setMapValue(schema, property, value)
		}
		if _, err := setNamedFields(schema, desiredFields); err != nil {
			return false, fmt.Errorf("Lidarr Slskd schema lacks field %s", missingField(err))
		}
		return true, l.send(ctx, http.MethodPost, path, schema)
	}
	return false, fmt.Errorf("Lidarr Slskd schema is unavailable at %s/schema", path)
}

func setNamedFields(resource map[string]any, desired map[string]any) (bool, error) {
	fields, ok := resource["fields"].([]any)
	if !ok {
		return false, fmt.Errorf("fields")
	}
	changed := false
	seen := make(map[string]bool, len(desired))
	for _, raw := range fields {
		field, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := textValue(field["name"])
		want, owned := desired[name]
		if !owned {
			continue
		}
		seen[name] = true
		changed = setMapValue(field, "value", want) || changed
	}
	for name := range desired {
		if !seen[name] {
			return false, fmt.Errorf("%s", name)
		}
	}
	return changed, nil
}

func missingField(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}

func setMapValue(resource map[string]any, key string, value any) bool {
	if reflect.DeepEqual(resource[key], value) {
		return false
	}
	resource[key] = value
	return true
}

func resourceID(resource map[string]any) (string, error) {
	switch id := resource["id"].(type) {
	case float64:
		return strconv.FormatInt(int64(id), 10), nil
	case int:
		return strconv.Itoa(id), nil
	case json.Number:
		return id.String(), nil
	default:
		return "", fmt.Errorf("Lidarr resource has no numeric id")
	}
}

func textValue(value any) string {
	text, _ := value.(string)
	return text
}

func (l Lidarr) get(ctx context.Context, path string, target any) error {
	return l.request(ctx, http.MethodGet, path, nil, target)
}

func (l Lidarr) send(ctx context.Context, method, path string, payload map[string]any) error {
	return l.request(ctx, method, path, payload, nil)
}

func (l Lidarr) request(ctx context.Context, method, path string, payload any, target any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(l.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("X-Api-Key", l.APIKey)
	request.Header.Set("Content-Type", "application/json")
	client := l.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Lidarr %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 8<<20)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		contents, _ := io.ReadAll(io.LimitReader(limited, lidarrErrorBodyLimit))
		_, _ = io.Copy(io.Discard, limited)
		if detail := strings.TrimSpace(string(contents)); detail != "" {
			return fmt.Errorf("Lidarr %s %s returned %s: %s", method, path, response.Status, detail)
		}
		return fmt.Errorf("Lidarr %s %s returned %s", method, path, response.Status)
	}
	if target == nil {
		_, err = io.Copy(io.Discard, limited)
		return err
	}
	if err := json.NewDecoder(limited).Decode(target); err != nil {
		return fmt.Errorf("decode Lidarr %s: %w", path, err)
	}
	return nil
}
