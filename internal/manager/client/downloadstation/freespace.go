package downloadstation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"ripperr/internal/model"
)

func (c *Client) FreeSpace(ctx context.Context, path string) (int64, error) {
	share, _, _ := strings.Cut(strings.TrimLeft(path, "/"), "/")
	if a, ok := c.api.apis[apiFileList]; !ok || a.Path == "" || share == "" {
		return 0, model.ErrFreeSpaceUnknown
	}
	var data struct {
		Shares []struct {
			Name       string `json:"name"`
			Additional struct {
				VolumeStatus struct {
					Freespace json.Number `json:"freespace"`
				} `json:"volume_status"`
			} `json:"additional"`
		} `json:"shares"`
	}
	err := c.api.call(ctx, apiFileList, "list_share", url.Values{"additional": {`["volume_status"]`}}, &data)
	var ae *apiError
	if errors.As(err, &ae) {
		return 0, fmt.Errorf("%w: %v", model.ErrFreeSpaceUnknown, err)
	}
	if err != nil {
		return 0, err
	}
	for _, s := range data.Shares {
		if strings.EqualFold(s.Name, share) {
			free, err := s.Additional.VolumeStatus.Freespace.Int64()
			if err != nil || free < 0 {
				return 0, model.ErrFreeSpaceUnknown
			}
			return free, nil
		}
	}
	return 0, model.ErrFreeSpaceUnknown
}
