// Package rfsnotify implements recursive folder monitoring by wrapping the excellent fsnotify library
package rfsnotify

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// ErrClosed is returned when adding a watch on a closed RWatcher.
var ErrClosed = errors.New("rfsnotify instance already closed")

// backend is the part of fsnotify.Watcher used by RWatcher.
type backend interface {
	Add(name string) error
	Remove(name string) error
	WatchList() []string
	Close() error
}

// recursiveBackend is a backend watching directory trees natively,
// directories created under a recursive watch are watched without RWatcher adding them.
type recursiveBackend interface {
	backend
	AddRecursive(name string) error
}

// notification is an fsnotify event or error.
type notification struct {
	event fsnotify.Event
	err   error
}

// RWatcher wraps fsnotify.Watcher. When fsnotify adds recursive watches, you should be able to switch your code to use fsnotify.Watcher
//
// On some backends (Windows), fsnotify Add and Remove are served by the goroutine producing events,
// so they must not be called by the goroutine reading them. RWatcher reads fsnotify notifications in one goroutine,
// which only queues them, and processes them in another one: it updates the watches an event requires,
// then forwards it. A created directory is therefore watched before its event is received.
//
// On Windows, RWatcher uses its own backend instead of fsnotify, which watches trees natively,
// at the cost of one handle and one buffer per watch: fsnotify needs one per directory.
// It only watches directories, and Add does not watch directories created later, unlike other platforms.
type RWatcher struct {
	Events chan fsnotify.Event
	Errors chan error

	fsnotify backend
	fsEvents <-chan fsnotify.Event
	fsErrors <-chan error

	mu      sync.Mutex
	pending []notification // unbounded, so that reading fsnotify never blocks
	wakeup  chan struct{}

	done          chan struct{}
	closeOnce     sync.Once
	closeErr      error
	readerDone    chan struct{}
	processorDone chan struct{}
}

// NewWatcher establishes a new watcher with the underlying OS and begins waiting for events.
func NewWatcher() (*RWatcher, error) {
	b, events, errs, err := newBackend()
	if err != nil {
		return nil, err
	}
	return newWatcher(b, events, errs), nil
}

func newWatcher(b backend, events <-chan fsnotify.Event, errs <-chan error) *RWatcher {
	m := &RWatcher{
		Events:        make(chan fsnotify.Event),
		Errors:        make(chan error),
		fsnotify:      b,
		fsEvents:      events,
		fsErrors:      errs,
		wakeup:        make(chan struct{}, 1),
		done:          make(chan struct{}),
		readerDone:    make(chan struct{}),
		processorDone: make(chan struct{}),
	}
	go m.readNotifications()
	go m.processNotifications()
	return m
}

// Add starts watching the named file or directory (non-recursively).
func (m *RWatcher) Add(name string) error {
	if m.isClosed() {
		return ErrClosed
	}
	return m.fsnotify.Add(name)
}

// AddRecursive starts watching the named directory and all sub-directories.
// Sub-directories removed while being walked are skipped.
func (m *RWatcher) AddRecursive(name string) error {
	if m.isClosed() {
		return ErrClosed
	}
	if rb, ok := m.fsnotify.(recursiveBackend); ok {
		return rb.AddRecursive(name)
	}
	return m.addRecursive(filepath.Clean(name))
}

// Remove stops watching the the named file or directory (non-recursively).
func (m *RWatcher) Remove(name string) error {
	return m.fsnotify.Remove(name)
}

// RemoveRecursive stops watching the named directory and all sub-directories.
// It relies on the watch list rather than on the file system, so it also works for deleted directories.
func (m *RWatcher) RemoveRecursive(name string) error {
	root := filepath.Clean(name)
	var errs []error
	found := false
	for _, path := range m.fsnotify.WatchList() {
		if !isWithin(path, root) {
			continue
		}
		found = true
		if err := m.fsnotify.Remove(path); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
			errs = append(errs, err)
		}
	}
	if !found {
		return fmt.Errorf("%w: %s", fsnotify.ErrNonExistentWatch, root)
	}
	return errors.Join(errs...)
}

// Close removes all watches, then closes the Events and Errors channels.
func (m *RWatcher) Close() error {
	m.closeOnce.Do(func() {
		close(m.done)
		// fsnotify (Windows) may never answer an Add or Remove still in flight once closed,
		// so wait for the processor first; fsnotify is still read meanwhile, so it can't block.
		<-m.processorDone
		m.closeErr = m.fsnotify.Close()
		if m.closeErr != nil {
			return
		}
		// fsnotify closes its channels, which stops the reader
		<-m.readerDone
	})
	return m.closeErr
}

func (m *RWatcher) isClosed() bool {
	select {
	case <-m.done:
		return true
	default:
		return false
	}
}

// readNotifications queues fsnotify notifications until fsnotify channels are closed, even after Close,
// so that fsnotify never blocks.
func (m *RWatcher) readNotifications() {
	defer close(m.readerDone)
	for {
		var n notification
		select {
		case e, ok := <-m.fsEvents:
			if !ok {
				return
			}
			n.event = e
		case err, ok := <-m.fsErrors:
			if !ok {
				return
			}
			n.err = err
		}
		if m.isClosed() {
			continue
		}
		m.mu.Lock()
		m.pending = append(m.pending, n)
		m.mu.Unlock()
		select {
		case m.wakeup <- struct{}{}:
		default:
		}
	}
}

// processNotifications updates the watches required by queued events, and forwards notifications, in order, until Close.
func (m *RWatcher) processNotifications() {
	defer func() {
		close(m.Events)
		close(m.Errors)
		close(m.processorDone)
	}()
	for {
		select {
		case <-m.done:
			return
		case <-m.wakeup:
		}
		m.mu.Lock()
		pending := m.pending
		m.pending = nil
		m.mu.Unlock()
		for _, n := range pending {
			if n.err != nil {
				if !m.sendError(n.err) {
					return
				}
				continue
			}
			if err := m.updateWatches(n.event); err != nil && !m.sendError(err) {
				return
			}
			select {
			case m.Events <- n.event:
			case <-m.done:
				return
			}
		}
	}
}

func (m *RWatcher) updateWatches(e fsnotify.Event) error {
	if _, ok := m.fsnotify.(recursiveBackend); ok {
		return nil
	}
	// Can't stat a deleted directory, so just pretend that it's always a directory and
	// try to remove from the watch list...  we really have no clue if it's a directory or not...
	if e.Has(fsnotify.Remove) {
		if err := m.fsnotify.Remove(e.Name); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
			return fmt.Errorf("could not stop watching %s: %w", e.Name, err)
		}
	}
	if e.Has(fsnotify.Create) {
		// the created entry may be gone already, its removal will be notified anyway
		if err := m.addRecursive(e.Name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("could not watch %s: %w", e.Name, err)
		}
	}
	return nil
}

func (m *RWatcher) sendError(err error) bool {
	select {
	case m.Errors <- err:
		return true
	case <-m.done:
		return false
	}
}

// addRecursive adds root, if it is a directory, and all directories under it to the watch list.
// Directories created in a new directory before it is watched are caught by the walk,
// but files created there in the meantime are not notified.
func (m *RWatcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			err = m.fsnotify.Add(path)
		}
		if err != nil && path != root && errors.Is(err, fs.ErrNotExist) {
			// removed while walking
			return nil
		}
		return err
	})
}

// isWithin reports whether path is root or one of its descendants.
func isWithin(path, root string) bool {
	if path == root {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(path, prefix)
}
