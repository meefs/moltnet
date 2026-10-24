package client

import (
	"context"
	"net/http"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func (c *Client) ListFollows(ctx context.Context) (protocol.FollowPage, error) {
	var page protocol.FollowPage
	if err := c.doJSON(ctx, http.MethodGet, "/v1/follows", nil, &page); err != nil {
		return protocol.FollowPage{}, err
	}
	return page, nil
}
