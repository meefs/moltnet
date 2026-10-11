package loop

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
	bridgeutil "github.com/noopolis/moltnet/internal/bridge"
	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

func (c *MoltnetClient) connect(
	ctx context.Context,
	config bridgeconfig.Config,
) (*websocket.Conn, *http.Response, error) {
	if err := c.resolveTokenErr(); err != nil {
		return nil, nil, err
	}
	if err := c.prepareOpenClaim(config); err != nil {
		return nil, nil, err
	}
	clockIdentity, err := config.Clock.Identity()
	if err != nil {
		return nil, nil, fmt.Errorf("moltnet attachment: %w", err)
	}

	endpoint, err := attachmentURL(c.baseURL)
	if err != nil {
		return nil, nil, err
	}

	headers := http.Header{}
	if token := c.currentToken(); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}

	connection, response, err := c.dialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		return nil, response, fmt.Errorf("dial moltnet attachment: %w", err)
	}
	if err := bridgeutil.CheckClockIdentity(response.Header, clockIdentity); err != nil {
		_ = connection.Close()
		return nil, response, fmt.Errorf("moltnet attachment: %w", err)
	}
	connection.SetReadLimit(maxAttachmentFrameBytes)

	return connection, response, nil
}
