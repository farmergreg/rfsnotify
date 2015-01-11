package rfsnotify

// Watcher matches the fsnotify v1 interface.
type FSWatcher interface {
	NewWatcher() (*Watcher, error)
	Add(name string) error
	Remove(name string) error
	Close() error
}
