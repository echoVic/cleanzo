package main

// GrowthAlert is an owner whose growth passed a threshold (spec §五).
type GrowthAlert struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Window  string `json:"window"` // "day" or "week"
	Days    int    `json:"days"`
	Bytes   int64  `json:"bytes"`
	AtLeast bool   `json:"atLeast"`
	Folder  string `json:"folder"` // where it grew most, down to a recorded child
	Total   int64  `json:"total"`  // what the owner takes now
	At      int64  `json:"at"`     // unix seconds of the record that raised it
}
