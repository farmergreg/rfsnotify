//go:build !windows

package rfsnotify

import "github.com/fsnotify/fsnotify"

func newBackend() (backend, <-chan fsnotify.Event, <-chan error, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, nil, err
	}
	return w, w.Events, w.Errors, nil
}
