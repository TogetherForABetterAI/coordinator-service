package models

// ScaleMessage represents a scaling request message
// from the scalability_queue
type ScaleMessage struct {
	ReplicaType string `json:"replica_type"` // "calibration" or "dispatcher"
}
