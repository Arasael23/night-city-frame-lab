//go:build !windows

package platform

import "errors"

var ErrPickCancelled = errors.New("cancelled")

func PickFolder(title, start string) (string, error) {
	return "", errors.New("the folder picker only exists on Windows")
}
