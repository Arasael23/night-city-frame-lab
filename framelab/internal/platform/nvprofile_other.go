//go:build !windows

package platform

import "errors"

// Dev stand-in for the NVIDIA profile (values marked as made up).
func NvProfile() ([]NvSetting, error) {
	if GPU().Vendor != "nvidia" {
		return nil, errors.New("no NVIDIA graphics card")
	}
	return []NvSetting{
		{Name: "Max frame rate", Value: "Off (dev)", From: "driver default"},
		{Name: "Max pre-rendered frames", Value: "App decides (dev)", From: "driver default", Note: "Low latency mode On sets 1"},
		{Name: "Vertical sync", Value: "App decides (dev)", From: "global"},
		{Name: "Power management mode", Value: "Normal (dev)", From: "driver default"},
		{Name: "Texture filtering quality", Value: "Quality (dev)", From: "driver default"},
		{Name: "G-SYNC", Value: "Allowed (dev)", From: "game profile"},
	}, nil
}
