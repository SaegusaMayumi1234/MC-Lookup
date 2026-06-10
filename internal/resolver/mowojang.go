package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/saegusamayumi1234/mc-lookup/internal/model"
)

// MowojangResolver resolves players using the Mowojang API
// Endpoint: https://mowojang.matdoes.dev/{username_or_uuid}
// Rate limit: ~10,000 requests per second (no practical limit)
// Note: Results are cached for 60 minutes on their end
type MowojangResolver struct {
	BaseResolver
}

type mowojangResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func NewMowojangResolver(timeout time.Duration, userAgent string) *MowojangResolver {
	return &MowojangResolver{
		BaseResolver: NewBaseResolver(constant.ResolverNameMowojang, timeout, userAgent),
	}
}

func (r *MowojangResolver) Resolve(ctx context.Context, identifier string) (*model.Player, error) {
	url := fmt.Sprintf("https://mowojang.matdoes.dev/%s", identifier)

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

	// Handle non-success status codes
	if resp.StatusCode != http.StatusOK {
		return nil, MapHTTPStatusToError(r.Name(), resp.StatusCode, identifier)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, NewUpstreamError(r.Name(), resp.StatusCode, err)
	}

	var data mowojangResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, NewUpstreamError(r.Name(), resp.StatusCode, err)
	}

	return &model.Player{
		UUID:     model.NormalizeUUID(data.ID),
		Username: data.Name,
	}, nil
}
