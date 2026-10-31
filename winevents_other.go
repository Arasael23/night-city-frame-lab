//go:build !windows

package platform

import "errors"

// WinEvents is a running Windows event recording (Windows only).
type WinEvents interface {
	Stop() WinEventStats
}

type DriverTotal struct {
	Name    string `json:"name"`
	TotalUs int64  `json:"total_us"`
	MaxUs   int64  `json:"max_us"`
	Count   uint64 `json:"count"`
}

type WinEventStats struct {
	Events       map[string]uint64 `json:"events"`
	Rows         uint64            `json:"rows"`
	EventsLost   uint32            `json:"events_lost"`
	BuffersLost  uint32            `json:"buffers_lost"`
	Drivers      int               `json:"drivers_known"`
	DriverTotals []DriverTotal     `json:"driver_totals,omitempty"`
	FirstQpc     int64             `json:"first_qpc"`
	LastQpc      int64             `json:"last_qpc"`
	Error        string            `json:"error,omitempty"`
}

func StartWinEvents(gamePid uint32, out string) (WinEvents, error) {
	return nil, errors.New("Windows only")
}

func StopWinEvents() {}
