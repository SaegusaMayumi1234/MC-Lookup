package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/saegusamayumi1234/mc-lookup/internal/model"
)

// PlayerDBResolver resolves players using the PlayerDB API
// Endpoint: https://playerdb.co/api/player/minecraft/{identifier}
// Rate limit: None (but requires User-Agent header)
type PlayerDBResolver struct {
	BaseResolver
}

type playerDBResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Player struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"player"`
	} `json:"data"`
	Success bool `json:"success"`
}

func NewPlayerDBResolver(timeout time.Duration, userAgent string) *PlayerDBResolver {
	return &PlayerDBResolver{
		BaseResolver: NewBaseResolver("playerdb", timeout, userAgent),
	}
}

func (r *PlayerDBResolver) Resolve(ctx context.Context, identifier string) (*model.Player, error) {
	url := fmt.Sprintf("https://playerdb.co/api/player/minecraft/%s", identifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, NewUpstreamError(r.Name(), 0, err)
	}

	req.Header.Set("User-Agent", r.UserAgent())

	resp, err := r.Client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, NewTimeoutError(r.Name(), err)
		}
		return nil, NewUpstreamError(r.Name(), 0, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		return nil, NewNotFoundError(r.Name(), identifier)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, NewUpstreamError(r.Name(), resp.StatusCode, err)
	}

	var data playerDBResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, NewUpstreamError(r.Name(), resp.StatusCode, err)
	}

	if !data.Success || data.Code != "player.found" {
		return nil, NewNotFoundError(r.Name(), identifier)
	}

	return &model.Player{
		UUID:     model.NormalizeUUID(data.Data.Player.ID),
		Username: data.Data.Player.Username,
	}, nil
}
