package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/saegusamayumi1234/mc-lookup/internal/model"
)

// MojangResolver resolves players using the official Mojang API
// Endpoint: https://api.mojang.com/users/profiles/minecraft/{username}
// Rate limit: 600 requests per 10 minutes
type MojangResolver struct {
	BaseResolver
}

type mojangResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func NewMojangResolver(timeout time.Duration, userAgent string) *MojangResolver {
	return &MojangResolver{
		BaseResolver: NewBaseResolver(constant.ResolverNameMojang, timeout, userAgent),
	}
}

func (r *MojangResolver) Resolve(ctx context.Context, identifier string) (*model.Player, error) {
	var url string
	if model.IsUUID(identifier) {
		// UUID lookup - use session server
		cleanUUID := strings.ReplaceAll(identifier, "-", "")
		url = fmt.Sprintf("https://sessionserver.mojang.com/session/minecraft/profile/%s", cleanUUID)
	} else {
		// Username lookup
		url = fmt.Sprintf("https://api.mojang.com/users/profiles/minecraft/%s", identifier)
	}

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

	var data mojangResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, NewUpstreamError(r.Name(), resp.StatusCode, err)
	}

	return &model.Player{
		UUID:     model.NormalizeUUID(data.ID),
		Username: data.Name,
	}, nil
}
