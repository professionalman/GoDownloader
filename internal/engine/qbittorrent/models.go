package qbittorrent

// qbTorrentInfo maps to /api/v2/torrents/info response.
type qbTorrentInfo struct {
	Hash          string  `json:"hash"`
	Name          string  `json:"name"`
	Size          int64   `json:"size"`
	Progress      float64 `json:"progress"` // 0.0-1.0
	DLSpeed       int64   `json:"dlspeed"`
	UPSpeed       int64   `json:"upspeed"`
	NumSeeds      int     `json:"num_seeds"`
	NumLeechs     int     `json:"num_leechs"`
	Uploaded      int64   `json:"uploaded"`
	Ratio         float64 `json:"ratio"`
	ETA           int64   `json:"eta"` // seconds, 8640000 = infinity
	State         string  `json:"state"`
	Category      string  `json:"category"`
	Tags          string  `json:"tags"`
	TotalSize     int64   `json:"total_size"`
	CompletedSize int64   `json:"completed"`
	SavePath      string  `json:"save_path"`
	ContentPath   string  `json:"content_path"`
	AddedOn       int64   `json:"added_on"`
	CompletionOn  int64   `json:"completion_on"`
	DLLimit       int64   `json:"dl_limit"`
	UPLimit       int64   `json:"up_limit"`
}

type qbTorrentProperties struct {
	IsPrivate   bool  `json:"is_private"`
	SeedingTime int64 `json:"seeding_time"`
}

type qbTracker struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	Tier   int    `json:"tier"`
}

type qbPreferences struct {
	ProxyType            int    `json:"proxy_type"`
	ProxyIP              string `json:"proxy_ip"`
	ProxyPort            int    `json:"proxy_port"`
	ProxyAuthEnabled     bool   `json:"proxy_auth_enabled"`
	ProxyUsername        string `json:"proxy_username"`
	ProxyPassword        string `json:"proxy_password"`
	ProxyHostnameLookup  bool   `json:"proxy_hostname_lookup"`
	ProxyBittorrent      bool   `json:"proxy_bittorrent"`
	ProxyPeerConnections bool   `json:"proxy_peer_connections"`
}

// qbTorrentFile maps to /api/v2/torrents/files response.
type qbTorrentFile struct {
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Progress float64 `json:"progress"` // 0.0-1.0
	Priority int     `json:"priority"` // 0=skip, 1=normal, 6=high, 7=max
	IsSeed   bool    `json:"is_seed"`
}

const (
	qbStateMetaDL             = "metaDL"
	qbStateForcedMetaDL       = "forcedMetaDL"
	qbStateDownloading        = "downloading"
	qbStateForcedDL           = "forcedDL"
	qbStateStalledDL          = "stalledDL"
	qbStateAllocating         = "allocating"
	qbStateQueuedDL           = "queuedDL"
	qbStateCheckingDL         = "checkingDL"
	qbStateStoppedDL          = "stoppedDL"
	qbStatePausedDL           = "pausedDL"
	qbStateUploading          = "uploading"
	qbStateForcedUP           = "forcedUP"
	qbStateStalledUP          = "stalledUP"
	qbStateQueuedUP           = "queuedUP"
	qbStateCheckingUP         = "checkingUP"
	qbStateStoppedUP          = "stoppedUP"
	qbStatePausedUP           = "pausedUP"
	qbStateError              = "error"
	qbStateMissingFiles       = "missingFiles"
	qbStateMoving             = "moving"
	qbStateCheckingResumeData = "checkingResumeData"
	qbStateUnknown            = "unknown"
)

// qBittorrent file priorities
const (
	qbPrioritySkip    = 0
	qbPriorityNormal  = 1
	qbPriorityHigh    = 6
	qbPriorityMaximum = 7
)
