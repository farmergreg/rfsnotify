// rfsnotify implements recursive folder monitoring using fsnotify
package rfsnotify

import (
	"gopkg.in/fsnotify.v1"
	"os"
	"path/filepath"
)

type RWatcher struct {
	Events chan fsnotify.Event
	Errors chan error

	done     chan bool
	fsnotify *fsnotify.Watcher
}

// New starts a new RWatcher
func New(pathToFolder string) (*RWatcher, error) {
	fsWatch, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	m := &RWatcher{}
	m.fsnotify = fsWatch
	m.Events = make(chan fsnotify.Event)
	m.Errors = make(chan error)
	m.done = make(chan bool)

	if err = m.watchRecursive(pathToFolder); err != nil {
		return nil, err
	}

	go m.start()

	return m, nil
}

func (m *RWatcher) start() {
	defer close(m.done)
	for {
		select {

		case e := <-m.fsnotify.Events:
			s, err := os.Stat(e.Name)
			if err == nil && s != nil && s.IsDir() {
				if e.Op&fsnotify.Create != 0 {
					m.watchRecursive(e.Name)
				}
			}
			//Can't stat a deleted directory, so just pretend that it's always a directory and
			//try to remove from the watch list...  we really have no clue if it's a directory or not...
			if e.Op&fsnotify.Remove != 0 {
				m.fsnotify.Remove(e.Name)
			}
			m.Events <- e

		case e := <-m.fsnotify.Errors:
			m.Errors <- e

		case <-m.done:
			m.fsnotify.Close()
			close(m.Events)
			close(m.Errors)
			return
		}
	}
}

// watchRecursive adds all directories under the given one to the watch list.
// this is probably a very racey process. What if a file is added to a folder before we get the watch added?
func (m *RWatcher) watchRecursive(path string) error {
	err := filepath.Walk(path, func(walkPath string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		} else if fi.IsDir() {
			err = m.fsnotify.Add(walkPath)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// Close releases all resources that are consumed by RWatcher
func (m *RWatcher) Close() {
	m.done <- true
}
