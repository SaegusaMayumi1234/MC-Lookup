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

// AshconResolver resolves players using the Ashcon API
// Endpoint: https://api.ashcon.app/mojang/v2/user/{identifier}
// Rate limit: Generous
type AshconResolver struct {
	BaseResolver
}

type ashconResponse struct {
	UUID     string `json:"uuid"`     // UUID with dashes
	Username string `json:"username"` // Username
}

func NewAshconResolver(timeout time.Duration, userAgent string) *AshconResolver {
	return &AshconResolver{
		BaseResolver: NewBaseResolver(constant.ResolverNameAshcon, timeout, userAgent),
	}
}

func (r *AshconResolver) Resolve(ctx context.Context, identifier string) (*model.Player, error) {
	url := fmt.Sprintf("https://api.ashcon.app/mojang/v2/user/%s", identifier)

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

	var data ashconResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, NewUpstreamError(r.Name(), resp.StatusCode, err)
	}

	return &model.Player{
		UUID:     model.NormalizeUUID(data.UUID),
		Username: data.Username,
	}, nil
}
