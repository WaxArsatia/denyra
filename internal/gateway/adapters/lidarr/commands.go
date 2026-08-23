package lidarr

import (
	"context"
	"encoding/json"
	"fmt"
)

type Command struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (c Client) StartAlbumSearch(ctx context.Context, albumID int64) (Command, []byte, error) {
	body, err := json.Marshal(map[string]any{"name": "AlbumSearch", "albumIds": []int64{albumID}})
	if err != nil {
		return Command{}, nil, err
	}
	var command Command
	err = c.Post(ctx, "/api/v1/command", body, &command)
	if err == nil && (command.ID <= 0 || command.Name != "AlbumSearch") {
		err = fmt.Errorf("malformed AlbumSearch response")
	}
	return command, body, err
}
func (c Client) Command(ctx context.Context, id int64) (Command, error) {
	var command Command
	err := c.Get(ctx, fmt.Sprintf("/api/v1/command/%d", id), nil, &command)
	if err == nil && command.ID != id {
		err = fmt.Errorf("command identity mismatch")
	}
	return command, err
}
