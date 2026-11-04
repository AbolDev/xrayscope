package classify

import "strings"

// orgHints maps the owner of an address (the organisation name of its
// network, as the ASN database reports it) to a service. It is only used for
// connections that carry no domain name, and only for owners whose networks
// serve one thing; a general host such as a cloud provider is left alone.
var orgHints = []struct {
	match    string
	category string
	service  string
}{
	{"telegram", "messaging", "Telegram"},
	{"whatsapp", "messaging", "WhatsApp"},
	{"discord", "messaging", "Discord"},
	{"facebook", "social", "Facebook"},
	{"meta platforms", "social", "Facebook"},
	{"twitter", "social", "X (Twitter)"},
	{"x corp", "social", "X (Twitter)"},
	{"snap inc", "social", "Snapchat"},
	{"tiktok", "social", "TikTok"},
	{"bytedance", "social", "TikTok"},
	{"linkedin", "social", "LinkedIn"},
	{"netflix", "video", "Netflix"},
	{"spotify", "music", "Spotify"},
	{"valve corp", "gaming", "Steam"},
	{"riot games", "gaming", "Riot Games"},
	{"blizzard", "gaming", "Blizzard"},
	{"sony interactive", "gaming", "PlayStation"},
	{"nintendo", "gaming", "Nintendo"},
	{"epic games", "gaming", "Epic Games"},
	{"supercell", "gaming", "Supercell"},
	{"zoom video", "work", "Zoom"},
	{"slack", "work", "Slack"},
	{"github", "dev", "GitHub"},
	{"openai", "ai", "ChatGPT"},
	{"anthropic", "ai", "Claude"},
	{"akamai", "cdn", "Akamai"},
	{"fastly", "cdn", "Fastly"},
}

// OrgHint classifies an address by the organisation that owns its network.
func OrgHint(org string) (category, service string, ok bool) {
	org = strings.ToLower(org)
	if org == "" {
		return "", "", false
	}
	for _, h := range orgHints {
		if strings.Contains(org, h.match) {
			return h.category, h.service, true
		}
	}
	return "", "", false
}
