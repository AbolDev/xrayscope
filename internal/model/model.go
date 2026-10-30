// Package model holds the wire types shared by the collector, the agent and
// the main server.
package model

// Rec is one aggregated access-log record: how many connections user U made
// from client IP to destination host D during unix minute M.
type Rec struct {
	M  int64  `json:"m"`
	U  string `json:"u"`
	IP string `json:"i"`
	D  string `json:"d"`
	N  int    `json:"n"`
}

// TrafficRec is an upload/download delta observed at unix time T.
type TrafficRec struct {
	T    int64  `json:"t"`
	U    string `json:"u"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// Batch is what an agent node sends to the main server.
type Batch struct {
	Node    string       `json:"node"`
	Version string       `json:"version,omitempty"`
	Panel   string       `json:"panel,omitempty"`
	Lines   int64        `json:"lines,omitempty"`
	Recs    []Rec        `json:"recs,omitempty"`
	Traffic []TrafficRec `json:"traffic,omitempty"`
	// Users carries panel-side account state (quota, expiry, limits).
	Users []PanelUser `json:"users,omitempty"`
}

// PanelUser is an account as known by the VPN panel.
type PanelUser struct {
	Email   string `json:"email"`
	Enabled bool   `json:"enabled"`
	Expiry  int64  `json:"expiry"` // unix seconds, 0 = never
	Quota   int64  `json:"quota"`  // bytes, 0 = unlimited
	Up      int64  `json:"up"`
	Down    int64  `json:"down"`
	LimitIP int    `json:"limit_ip"`
	Inbound string `json:"inbound,omitempty"`
}
