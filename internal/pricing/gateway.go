package pricing

// RawUsage is one raw-id token breakdown inside a canonical model.
type RawUsage struct {
	Turns      int64 `json:"turns"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	Total      int64 `json:"total"`
}
