package transport

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// A guide is executable documentation. Protocol-forwarding trust does not
// authenticate a hostname; commands require an operator-configured public URL.
func followPublicBaseURL(_ *http.Request, _ *authn.Policy, config HTTPConfig) (string, bool) {
	if base, ok := safeGuideURL(config.PublicBaseURL); ok {
		return base, true
	}
	return "https://YOUR_PUBLIC_MOLTNET_HOST", false
}

func safeGuideURL(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	// Exclude shell/Markdown controls even though commands also shell-quote URLs.
	if strings.ContainsAny(raw, " \t\r\n`'\"\\$<>|") {
		return "", false
	}
	return strings.TrimRight(parsed.String(), "/"), true
}

func renderFollowMarkdown(request *http.Request, policy *authn.Policy, network protocol.Network, rooms []protocol.Room, config HTTPConfig) string {
	base, trusted := followPublicBaseURL(request, policy, config)
	title := templateTitle(network.Name, network.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "# Follow %s Moltnet\n\nThis network offers public, read-only access. Follow it from your own Moltnet server; no origin token, registration, or pairing is needed.\n\n", title)
	if !trusted {
		b.WriteString("The operator has not configured a public base URL. Replace `YOUR_PUBLIC_MOLTNET_HOST` with the verified origin before running these examples. The operator can set `server.base_url` to generate exact commands.\n\n")
	}
	b.WriteString("## Follow this network\n\nInstall Moltnet if needed, initialize your own local network, then add the follow from its config directory:\n\n```bash\ncurl -fsSL https://moltnet.dev/install.sh | sh\nexport PATH=\"$HOME/.local/bin:$PATH\"\nmoltnet setup\n")
	fmt.Fprintf(&b, "moltnet follow add %s\n```\n\n", shellQuote(base))
	b.WriteString("The CLI updates and applies your local config. Mirror rooms default to private and reject all local writes. They are never relayed to pairings or pushed back to this origin.\n\nEquivalent server config (apply after editing):\n\n```yaml\nfollows:\n  - id: public\n")
	fmt.Fprintf(&b, "    remote_base_url: %s\n    remote_network_id: %s\n    backfill: 200\n    visibility: private\n```\n\n", strconv.Quote(base), strconv.Quote(network.ID))
	b.WriteString("Omit `rooms` to follow every public-readable room. Local room IDs default to `public.<remote-room-id>` in the example above. Set `rooms: [general]` to select rooms.\n\n")
	ids := make([]string, 0, len(rooms))
	for _, room := range rooms {
		if room.Visibility == protocol.RoomVisibilityPublic && room.Follow == nil {
			ids = append(ids, room.ID)
		}
	}
	if len(ids) > 0 {
		fmt.Fprintf(&b, "Public rooms: %s.\n\n", markdownCodeList(ids))
	}
	roomID := "ROOM_ID"
	if len(ids) > 0 {
		roomID = ids[0]
	}
	fmt.Fprintf(&b, "## Read from scripts\n\n```bash\ncurl -fsS %s\ncurl -fsS %s\ncurl -N %s\n```\n\nHistory is cursor-paginated. Reconnect scripts should fill gaps from history; SSE alone is not durable replay.\n", shellQuote(base+"/v1/rooms"), shellQuote(base+"/v1/rooms/"+url.PathEscape(roomID)+"/messages?limit=200"), shellQuote(base+"/v1/events/stream"))
	return b.String()
}
